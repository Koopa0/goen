package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/ordernumber"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	outbox  *outbox.Store
	images  *media.Handler
	letters *newsletter.Store
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where no session was ever opened.
	sessions SessionCloser
	storeMap *cart.StoreMap
	store    *Store
	log      *slog.Logger
}

type SessionCloser interface {
	ExpireSession(ctx context.Context, sessionID string) error
}

type HandlerDeps struct {
	Store    *Store
	Images   *media.Handler
	Outbox   *outbox.Store
	Letters  *newsletter.Store
	Log      *slog.Logger
	Sessions SessionCloser
	// StoreMap decides whether checkout offers pickup-point methods; nil is a
	// deployment with no map.
	StoreMap *cart.StoreMap
}

func NewHandler(d HandlerDeps) *Handler {
	if d.Store == nil || d.Images == nil || d.Outbox == nil || d.Letters == nil || d.Log == nil {
		panic("admin: NewHandler requires a store, a media handler, an outbox, " +
			"a newsletter store and a logger")
	}
	return &Handler{
		store: d.Store, images: d.Images, outbox: d.Outbox, letters: d.Letters,
		log: d.Log, sessions: d.Sessions, storeMap: d.StoreMap,
	}
}

// closeSessions expires the checkouts a cancelled order left open at Stripe,
// post-commit and best effort — the stock and the credit are already back.
func (h *Handler) closeSessions(ctx context.Context, number string, sessions []string) {
	if h.sessions == nil {
		return
	}

	// The database cancellation has already committed. Keep request values for
	// tracing, but do not let a client disconnect turn the provider cleanup into
	// a no-op. One short budget bounds the whole best-effort batch.
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	for _, id := range sessions {
		if err := h.sessions.ExpireSession(ctx, id); err != nil {
			// Warn, not Error: Stripe refuses to expire anything but an OPEN
			// session, so a checkout completed a moment ago lands here.
			h.log.WarnContext(ctx, "expire checkout session of a cancelled order",
				"order_number", number, "session_id", id, "error", err)
		}
	}
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dashboard", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Dashboard(admin.Meta(r.Context()), view))
}

func (h *Handler) Orders(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Orders(r.Context(),
		ParseQueueFilter(r.URL.Query().Get("status")), r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read orders", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Orders(admin.OrdersMeta(r.Context()), view))
}

func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNoOrderTitle)}, "404",
				i18n.T(r.Context(), i18n.KeyAdminNoOrderHead),
				i18n.T(r.Context(), i18n.KeyAdminNoOrderBody)))
			return
		}
		h.log.ErrorContext(r.Context(), "read order", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	view.AllowanceOperationID = uuid.NewString()
	web.Render(w, r, h.log, http.StatusOK,
		admin.Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

func (h *Handler) AdvanceOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	sessions, err := h.store.Advance(r.Context(), number, ParseStatus(r.PostFormValue("status")), staffID(r))
	switch {
	case err == nil:
		h.closeSessions(r.Context(), number, sessions)
		http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
	case errors.Is(err, ErrPaidCancel), pgerr.IsConstraint(err, "orders_paid_cancel_needs_refund"):
		http.Redirect(w, r, "/admin/orders/"+number+"?paidcancel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
	case pgerr.IsConstraint(err, "orders_funded_to_leave_pending"):
		http.Redirect(w, r, "/admin/orders/"+number+"?unfunded=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
	case pgerr.IsConstraint(err, "orders_finished_when_shipped"):
		http.Redirect(w, r, "/admin/orders/"+number+"?owesparcel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
	case errors.Is(err, ErrRefused):
		// Logged in full; the page names the rule only for the refusals a shop
		// assistant can act on, because a constraint name is not one of them.
		h.log.WarnContext(r.Context(), "order transition refused",
			"order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
	default:
		h.log.ErrorContext(r.Context(), "advance order", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) Ship(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}

	lines, parseErr := parcelLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "dispatch rejected", "order", number, "error", parseErr)
		h.rejectShip(w, r, &shipRefusal{quantity: i18n.KeyAdminNoticeBadParcel})
		return
	}

	err := h.store.Ship(r.Context(), number, Dispatch{
		Carrier:  r.PostFormValue("carrier"),
		Tracking: r.PostFormValue("tracking"),
		Lines:    lines,
	}, staffID(r))
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?shipped=1", http.StatusSeeOther)
	case pgerr.IsConstraint(err, "order_shipments_tracking_key"):
		h.rejectShip(w, r, &shipRefusal{tracking: i18n.KeyAdminTrackingTaken})
	case errors.Is(err, ErrQuantity):
		h.rejectShip(w, r, &shipRefusal{quantity: i18n.KeyAdminNoticeBadParcel})
	case errors.Is(err, ErrCarrier):
		h.rejectShip(w, r, &shipRefusal{carrier: i18n.KeyAdminCarrierNotForOrder})
	case errors.Is(err, ErrInvalid):
		refusal := shipRefusal{}
		if !carrier.Carrier(strings.TrimSpace(r.PostFormValue("carrier"))).Known() {
			refusal.carrier = i18n.KeyAdminNoticeNeeds
		}
		if strings.TrimSpace(r.PostFormValue("tracking")) == "" {
			refusal.tracking = i18n.KeyAdminNoticeNeeds
		}
		h.rejectShip(w, r, &refusal)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipment refused", "order", number, "error", err)
		h.rejectShip(w, r, &shipRefusal{notice: i18n.KeyAdminNoticeRefused})
	default:
		h.log.ErrorContext(r.Context(), "ship order", "error", err)
		access.ServerError(w, r, h.log)
	}
}

type shipRefusal struct {
	carrier, tracking, quantity, notice i18n.Key
}

// rejectShip re-renders the order with everything staff typed, the carrier, the
// tracking number and each line's quantity, and the refused control marked,
// because re-typing a long tracking number after every mistake is the cost a
// redirect would put on the warehouse.
func (h *Handler) rejectShip(w http.ResponseWriter, r *http.Request, refusal *shipRefusal) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read order after refused dispatch", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	say := func(k i18n.Key) string {
		if k == "" {
			return ""
		}
		return i18n.T(r.Context(), k)
	}
	view.ShipCarrier = r.PostFormValue("carrier")
	view.ShipTracking = r.PostFormValue("tracking")
	view.ShipCarrierError = say(refusal.carrier)
	view.TrackingError = say(refusal.tracking)
	view.ShipQtyError = say(refusal.quantity)
	view.Notice = say(refusal.notice)
	view.ShipQty = map[string]string{}
	for i := range view.Shippable {
		id := view.Shippable[i].OrderLineID
		if typed, ok := r.PostForm["qty_"+id]; ok && len(typed) > 0 {
			view.ShipQty[id] = typed[0]
		}
	}
	view.AllowanceOperationID = uuid.NewString()
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		admin.Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

// parcelLines reads the `qty_<order_line_id>` fields; a nil map means everything
// outstanding. Keyed by id because a browser may reorder repeated fields.
func parcelLines(r *http.Request) (map[uuid.UUID]int32, error) {
	var out map[uuid.UUID]int32
	for name, values := range r.PostForm {
		rest, ok := strings.CutPrefix(name, "qty_")
		if !ok || len(values) == 0 {
			continue
		}
		lineID, err := uuid.Parse(rest)
		if err != nil {
			return nil, fmt.Errorf("field %q does not name an order line: %w", name, err)
		}
		raw := strings.TrimSpace(values[0])
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("quantity on %s: %w", rest, err)
		}
		if n < 0 {
			return nil, fmt.Errorf("quantity on %s is negative", rest)
		}
		if out == nil {
			out = make(map[uuid.UUID]int32, 4)
		}
		out[lineID] = int32(n)
	}
	return out, nil
}

// staffID is who is acting. An unparseable id records "nobody" rather than
// failing a dispatch that has physically happened.
func staffID(r *http.Request) uuid.NullUUID {
	u, ok := account.FromContext(r.Context())
	if !ok {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

func (h *Handler) StaffNote(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	if err := h.store.SetStaffNote(r.Context(), number, r.PostFormValue("note")); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "set staff note", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by ordernumber.Valid
}

func (h *Handler) Variants(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Variants(r.Context(), r.URL.Query().Get("low") == "1", r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	view.Return = stockReturn(r.URL.Query().Get("low"), view.Term, r.URL.Query().Get(web.KeysetParam), "", "")
	web.Render(w, r, h.log, http.StatusOK, admin.Variants(admin.VariantsMeta(r.Context()), view))
}

func (h *Handler) AdjustStock(w http.ResponseWriter, r *http.Request) {
	u, _ := account.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	delta, ok := ParseAdjustment(r.PostFormValue("delta"))
	if !ok {
		h.rejectAdjustment(w, r, i18n.KeyAdminStockDeltaError)
		return
	}
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
	}

	err := h.store.AdjustStock(r.Context(), r.PostFormValue("sku"), delta, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "stock adjustment refused",
			"sku", r.PostFormValue("sku"), "delta", delta, "error", err)
		h.rejectAdjustment(w, r, i18n.KeyAdminStockAdjustRefused)
	default:
		h.log.ErrorContext(r.Context(), "adjust stock", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectAdjustment(w http.ResponseWriter, r *http.Request, key i18n.Key) {
	var low, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		low, term, after = u.Query().Get("low"), u.Query().Get("q"), u.Query().Get(web.KeysetParam)
	}
	view, err := h.store.Variants(r.Context(), low == "1", term, after)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants after refused adjustment", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Return = stockReturn(low, view.Term, after, "", "")
	sku := r.PostFormValue("sku")
	shown := false
	for i := range view.Variants {
		if view.Variants[i].SKU == sku {
			view.Variants[i].DraftDelta = r.PostFormValue("delta")
			view.Variants[i].DeltaError = i18n.T(r.Context(), key)
			shown = true
		}
	}
	if !shown {
		// The row is not on this page, so the banner has to say it.
		view.Notice = i18n.T(r.Context(), key)
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Variants(admin.VariantsMeta(r.Context()), view))
}

func (h *Handler) ReceiveStock(w http.ResponseWriter, r *http.Request) {
	u, _ := account.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	sku := r.PostFormValue("sku")
	back := "/admin/stock/" + url.PathEscape(sku)

	quantity, ok := ParseReceipt(r.PostFormValue("quantity"))
	if !ok {
		http.Redirect(w, r, back+"?badqty=1", http.StatusSeeOther)
		return
	}
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
	}

	err := h.store.ReceiveStock(r.Context(), sku, quantity, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, back+"?received=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "goods receipt refused",
			"sku", sku, "quantity", quantity, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "receive stock", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetVariantActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetVariantActive(r.Context(),
		r.PostFormValue("sku"), r.PostFormValue("active") == "1")
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "variant activation refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	default:
		h.log.ErrorContext(r.Context(), "set variant active", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetVariantPrice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	price, okPrice := ParsePrice(r.PostFormValue("price"))
	compare, okCompare := ParsePrice(r.PostFormValue("compare_at"))
	if !okPrice || !okCompare || price <= 0 {
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
		return
	}

	err := h.store.SetVariantPrice(r.Context(), r.PostFormValue("sku"), price, compare)
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "reprice refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, stockBack(r, "refused"), http.StatusSeeOther) //nolint:gosec // G710: stockBack answers /admin/stock with only an encoded query
	default:
		h.log.ErrorContext(r.Context(), "set variant price", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func stockReturn(low, term, after, notice, sku string) string {
	q := url.Values{}
	if low == "1" {
		q.Set("low", "1")
	}
	if term != "" {
		q.Set("q", term)
	}
	if after != "" {
		q.Set(web.KeysetParam, after)
	}
	if notice != "" {
		q.Set(notice, "1")
	}
	target := "/admin/stock"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	if sku != "" {
		target += "#row-" + url.PathEscape(sku)
	}
	return target
}

// stockBack is where a write on the stock list returns to: the filter and page
// the form was posted from, with the notice and the row anchored. The posted
// value is only ever read for those two parameters, so it cannot name another
// address.
func stockBack(r *http.Request, notice string) string {
	var low, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		low, term, after = u.Query().Get("low"), web.SearchTerm(u.Query().Get("q")), u.Query().Get(web.KeysetParam)
	}
	return stockReturn(low, term, after, notice, r.PostFormValue("sku"))
}

var adminNotices = map[string]i18n.Key{
	"ok":             i18n.KeyAdminNoticeOK,
	"refused":        i18n.KeyAdminNoticeRefused,
	"shipped":        i18n.KeyAdminNoticeShipped,
	"toolate":        i18n.KeyAdminNoticeTooLate,
	"shippingneeds":  i18n.KeyAdminNoticeShippingNeeds,
	"deliveryneeds":  i18n.KeyAdminNoticeDeliveryNeeds,
	"imageneeds":     i18n.KeyAdminNoticeImageNeeds,
	"toobig":         i18n.KeyAdminNoticeTooBig,
	"notimage":       i18n.KeyAdminNoticeNotImage,
	"losslesswebp":   i18n.KeyAdminNoticeLosslessWebP,
	"uploadfailed":   i18n.KeyAdminNoticeUploadFailed,
	"uploadbusy":     i18n.KeyAdminNoticeUploadBusy,
	"inuse":          i18n.KeyAdminNoticeInUse,
	"attachrefused":  i18n.KeyAdminNoticeAttachRefused,
	"noalt":          i18n.KeyAdminNoticeNoAlt,
	"badoption":      i18n.KeyAdminNoticeBadOption,
	"nodiscount":     i18n.KeyAdminNoticeNoDiscount,
	"refundfailed":   i18n.KeyAdminNoticeRefundFailed,
	"paidcancel":     i18n.KeyAdminNoticePaidCancel,
	"refunded":       i18n.KeyAdminNoticeRefunded,
	"refundpending":  i18n.KeyAdminNoticeRefundPending,
	"cancelinvoice":  i18n.KeyAdminNoticeCancelInvoice,
	"refundretry":    i18n.KeyAdminNoticeRefundRetry,
	"received":       i18n.KeyAdminNoticeReceived,
	"badqty":         i18n.KeyAdminNoticeBadQty,
	"inspected":      i18n.KeyAdminNoticeInspected,
	"closed":         i18n.KeyAdminNoticeClosed,
	"assessed":       i18n.KeyAdminNoticeAssessed,
	"unfunded":       i18n.KeyAdminNoticeUnfunded,
	"owesparcel":     i18n.KeyAdminNoticeOwesParcel,
	"invoiced":       i18n.KeyAdminNoticeInvoiced,
	"voided":         i18n.KeyAdminNoticeVoided,
	"hasinvoice":     i18n.KeyAdminNoticeHasInvoice,
	"noinvoice":      i18n.KeyAdminNoticeNoInvoice,
	"invoicefailed":  i18n.KeyAdminNoticeInvoiceFailed,
	"invoicepending": i18n.KeyAdminNoticeInvoicePending,
	"allowed":        i18n.KeyAdminNoticeAllowed,
	"allowtoomuch":   i18n.KeyAdminNoticeAllowTooMuch,
	"allowclaimed":   i18n.KeyAdminNoticeAllowClaimed,
	"voidreason":     i18n.KeyAdminNoticeVoidReason,
	"voidfailed":     i18n.KeyAdminNoticeVoidFailed,
	"allowfailed":    i18n.KeyAdminNoticeAllowFailed,
	"saved":          i18n.KeyAdminNoticeSaved,
	"sent":           i18n.KeyAdminNoticeSent,
	"already":        i18n.KeyAdminNoticeAlready,
	"specfailed":     i18n.KeyAdminNoticeSpecFailed,
}

func noticeFor(r *http.Request) string {
	return web.Notice(r, adminNotices)
}

func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// small preserves malformed input as an invalid negative sentinel until the
// form's Validate method can attribute the refusal. Banner and hero use zero to
// mean "no end date", so silently collapsing unreadable input to zero would
// turn a typo into an unbounded promotion.
func small(s string) int32 {
	n, ok := web.ParseCount(s)
	if !ok {
		return -1
	}
	return n
}

func (h *Handler) Campaigns(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Campaigns(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaigns", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Campaigns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
}

func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &CampaignForm{
		Slug:    r.PostFormValue("slug"),
		Title:   r.PostFormValue("title"),
		TitleEn: r.PostFormValue("title_en"),
		Days:    small(r.PostFormValue("days")),
		Tone:    r.PostFormValue("tone"),
	}
	errs, err := h.store.CreateCampaign(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create campaign", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		view, readErr := h.store.Campaigns(r.Context(), r.URL.Query().Get(web.KeysetParam))
		if readErr != nil {
			access.ServerError(w, r, h.log)
			return
		}
		view.Errors = errs
		view.Draft = admin.CampaignDraft{
			Slug: f.Slug, Title: f.Title, TitleEn: f.TitleEn, Days: r.PostFormValue("days"), Tone: f.Tone,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Campaigns(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
	default:
		//nolint:gosec // G710: slug matched slugFormat in Validate
		http.Redirect(w, r, "/admin/campaigns/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) EditCampaign(w http.ResponseWriter, r *http.Request) {
	h.renderCampaign(w, r, http.StatusOK, noticeFor(r), nil)
}

func (h *Handler) renderCampaign(w http.ResponseWriter, r *http.Request, status int, notice string, errs map[string]string) {
	slug := r.PathValue("slug")
	products, err := h.store.CampaignProducts(r.Context(), slug)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign products", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	image, tone, err := h.store.CampaignImage(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "read campaign image", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	detail, err := h.store.CampaignDetail(r.Context(), slug)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if errs["window"] != "" {
		detail.StartsAtInput, detail.EndsAtInput = r.PostFormValue("starts_at"), r.PostFormValue("ends_at")
	}
	term := web.SearchTerm(r.URL.Query().Get("find"))
	matches, err := h.store.SearchCampaignProducts(r.Context(), slug, term)
	if err != nil {
		h.log.ErrorContext(r.Context(), "search campaign products", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, status, admin.CampaignForm(
		layouts.Page{Title: detail.Title}, admin.CampaignView{
			Slug: slug, CampaignDetail: detail, Term: term, Matches: matches,
			Products: products, Notice: notice, Image: image, Tone: tone, Errors: errs,
		}))
}

func (h *Handler) SetCampaignWindow(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	errs, err := h.store.SetCampaignWindow(r.Context(), slug,
		r.PostFormValue("starts_at"), r.PostFormValue("ends_at"))
	switch {
	case err == nil && len(errs) > 0:
		h.renderCampaign(w, r, http.StatusUnprocessableEntity, "", errs)
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "set campaign window", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetCampaignTone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.SetCampaignTone(r.Context(), slug, r.PostFormValue("tone")); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		h.renderCampaign(w, r, http.StatusUnprocessableEntity, "", map[string]string{"tone": i18n.T(r.Context(), i18n.KeyFormToneUnknown)})
	default:
		h.log.ErrorContext(r.Context(), "set campaign tone", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) SetCampaignImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.StoreUpload(w, r, "image")
	if err != nil {
		h.log.WarnContext(r.Context(), "campaign image upload", "error", err, "slug", slug)
		reason := i18n.KeyAdminNoticeUploadFailed
		switch {
		case errors.Is(err, media.ErrTooLarge):
			reason = i18n.KeyAdminNoticeTooBig
		case errors.Is(err, media.ErrNotAnImage):
			reason = i18n.KeyAdminNoticeNotImage
		}
		h.renderCampaign(w, r, http.StatusUnprocessableEntity, "", map[string]string{"image": i18n.T(r.Context(), reason)})
		return
	}
	err = h.store.SetCampaignImage(r.Context(), slug, obj.Digest, r.PostFormValue("alt"), r.PostFormValue("alt_en"))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		field, reason := "alt", i18n.KeyFormHeroAlt
		if utf8.RuneCountInString(strings.TrimSpace(r.PostFormValue("alt_en"))) > MaxCampaignAltRunes {
			field, reason = "alt_en", i18n.KeyFormCampaignAltEnLong
		}
		h.renderCampaign(w, r, http.StatusUnprocessableEntity, "", map[string]string{field: i18n.T(r.Context(), reason)})
	default:
		h.log.ErrorContext(r.Context(), "set campaign image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) RemoveCampaignImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.ClearCampaignImage(r.Context(), slug); {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "remove campaign image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) FeatureProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	var err error
	if r.PostFormValue("action") == "remove" {
		err = h.store.UnfeatureProduct(r.Context(), slug, r.PostFormValue("product"))
	} else {
		err = h.store.FeatureProduct(r.Context(), slug, r.PostFormValue("product"))
	}
	if err != nil {
		h.log.WarnContext(r.Context(), "feature product", "campaign", slug, "error", err)
		back := "/admin/campaigns/" + slug
		if errors.Is(err, ErrNotFound) {
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther)
			return
		}
		// sale_campaign_needs_discount: nothing is marked down.
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, back+"?nodiscount=1", http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) SetCampaignActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	back := "/admin/campaigns"
	if r.PostFormValue("back") == "detail" {
		back += "/" + slug
	}
	if err := h.store.SetCampaignActive(r.Context(), slug,
		r.PostFormValue("active") == "true"); err != nil {
		h.log.WarnContext(r.Context(), "set campaign active", "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: slug is the route's own path value
		return
	}
	http.Redirect(w, r, back+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: slug is the route's own path value
}

func (h *Handler) Movements(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Movements(r.Context(), r.PathValue("sku"), r.URL.Query().Get(web.KeysetParam))
	switch {
	case err == nil:
		view.Notice = noticeFor(r)
		web.Render(w, r, h.log, http.StatusOK, admin.Movements(
			layouts.Page{Title: view.SKU}, &view))
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "read movements", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.FAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

func (h *Handler) CreateFAQEntry(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := faqFormOf(r)
	errs, err := h.store.CreateFAQEntry(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create faq entry", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) EditFAQEntry(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if r.PostFormValue("action") == "delete" {
		if err := h.store.DeleteFAQEntry(r.Context(), id); err != nil {
			h.log.WarnContext(r.Context(), "delete faq entry", "error", err)
			access.NotFound(w, r, h.log)
			return
		}
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
		return
	}

	f := faqFormOf(r)
	f.ID = id
	errs, err := h.store.UpdateFAQEntry(r.Context(), f)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update faq entry", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

func faqFormOf(r *http.Request) *FAQForm {
	return &FAQForm{
		Category:   r.PostFormValue("category"),
		Question:   r.PostFormValue("question"),
		Answer:     r.PostFormValue("answer"),
		CategoryEn: r.PostFormValue("category_en"),
		QuestionEn: r.PostFormValue("question_en"),
		AnswerEn:   r.PostFormValue("answer_en"),
	}
}

func (h *Handler) rejectFAQ(
	w http.ResponseWriter, r *http.Request, f *FAQForm, errs map[string]string,
) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	typed := admin.FAQEntry{
		ID:       f.ID,
		Category: f.Category, Question: f.Question, Answer: f.Answer,
		CategoryEn: f.CategoryEn, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
	}
	// An edit goes back to the entry it was made on. The add form's draft is the
	// wrong place: its error would send the operator to press 新增 and publish a
	// second copy while the original stays unedited.
	if f.ID != "" {
		view.Edit, view.EditErrors = typed, errs
	} else {
		view.Draft, view.Errors = typed, errs
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.FAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

func (h *Handler) HomeContent(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read hero slides", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	banners, err := h.store.Banners(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read promo banners", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Banners = banners
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) CreateBanner(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &BannerForm{
		Message:    r.PostFormValue("message"),
		Short:      r.PostFormValue("short"),
		Code:       r.PostFormValue("code"),
		CTALabel:   r.PostFormValue("cta_label"),
		CTAHref:    r.PostFormValue("cta_href"),
		MessageEn:  r.PostFormValue("message_en"),
		ShortEn:    r.PostFormValue("short_en"),
		CTALabelEn: r.PostFormValue("cta_label_en"),
		Days:       small(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateBanner(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create promo banner", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectBanner(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) SetBannerActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetBannerActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "1")
	if err != nil {
		h.log.WarnContext(r.Context(), "toggle promo banner", "error", err)
		access.NotFound(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
}

func (h *Handler) rejectBanner(
	w http.ResponseWriter, r *http.Request, f *BannerForm, errs map[string]string,
) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	if banners, bannerErr := h.store.Banners(r.Context()); bannerErr == nil {
		view.Banners = banners
	}
	view.Errors = errs
	view.BannerDraft = admin.BannerDraft{
		Message: f.Message, Short: f.Short, Code: f.Code,
		CTALabel: f.CTALabel, CTAHref: f.CTAHref, Days: r.PostFormValue("days"),
		MessageEn: f.MessageEn, ShortEn: f.ShortEn, CTALabelEn: f.CTALabelEn,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

// CreateHeroSlide takes the artwork with the copy, multipart. The image is
// optional and a slide with none falls back to the built-in artwork; the copy is
// checked before the image is decoded, so a refused slide stores nothing.
func (h *Handler) CreateHeroSlide(w http.ResponseWriter, r *http.Request) {
	upload, err := h.images.OpenUpload(w, r, "image")
	if err != nil {
		h.log.WarnContext(r.Context(), "hero image", "error", err)
		http.Redirect(w, r, "/admin/home?"+uploadReason(err), http.StatusSeeOther)
		return
	}
	defer upload.Close()

	f := &HeroForm{
		Eyebrow:        r.PostFormValue("eyebrow"),
		Headline:       r.PostFormValue("headline"),
		Body:           r.PostFormValue("body"),
		PrimaryLabel:   r.PostFormValue("primary_label"),
		PrimaryHref:    r.PostFormValue("primary_href"),
		SecondLabel:    r.PostFormValue("second_label"),
		SecondHref:     r.PostFormValue("second_href"),
		ImageChosen:    upload != nil,
		ImageAlt:       r.PostFormValue("alt"),
		EyebrowEn:      r.PostFormValue("eyebrow_en"),
		HeadlineEn:     r.PostFormValue("headline_en"),
		BodyEn:         r.PostFormValue("body_en"),
		PrimaryLabelEn: r.PostFormValue("primary_label_en"),
		SecondLabelEn:  r.PostFormValue("second_label_en"),
		ImageAltEn:     r.PostFormValue("alt_en"),
		Days:           small(r.PostFormValue("days")),
	}
	if errs := f.Validate(r.Context()); len(errs) > 0 {
		h.rejectHeroSlide(w, r, f, errs)
		return
	}
	if upload != nil {
		obj, storeErr := upload.Store(r.Context())
		// A file no decoder accepts leaves the slide on the built-in artwork.
		if storeErr != nil && !errors.Is(storeErr, media.ErrNotAnImage) {
			h.log.WarnContext(r.Context(), "hero image", "error", storeErr)
			http.Redirect(w, r, "/admin/home?"+uploadReason(storeErr), http.StatusSeeOther)
			return
		}
		f.ImageKey = obj.Digest
	}

	errs, err := h.store.CreateHeroSlide(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create hero slide", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectHeroSlide(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) rejectHeroSlide(
	w http.ResponseWriter, r *http.Request, f *HeroForm, errs map[string]string,
) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.Draft = admin.HeroDraft{
		Eyebrow: f.Eyebrow, Headline: f.Headline, Body: f.Body,
		PrimaryLabel: f.PrimaryLabel, PrimaryHref: r.PostFormValue("primary_href"),
		SecondLabel: f.SecondLabel, SecondHref: r.PostFormValue("second_href"),
		ImageKey: f.ImageKey, ImageAlt: f.ImageAlt, Days: r.PostFormValue("days"),
		EyebrowEn: f.EyebrowEn, HeadlineEn: f.HeadlineEn, BodyEn: f.BodyEn,
		PrimaryLabelEn: f.PrimaryLabelEn, SecondLabelEn: f.SecondLabelEn,
		ImageAltEn: f.ImageAltEn,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Home(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

func (h *Handler) SetHeroSlideActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if err := h.store.SetHeroSlideActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "true"); err != nil {
		h.log.WarnContext(r.Context(), "toggle hero slide", "error", err)
		http.Redirect(w, r, "/admin/home?refused=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
}

func (h *Handler) PromoteHeroSlide(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if err := h.store.PromoteHeroSlide(r.Context(), r.PathValue("id")); err != nil {
		h.log.WarnContext(r.Context(), "promote hero slide", "error", err)
		http.Redirect(w, r, "/admin/home?refused=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
}

func (h *Handler) Taxonomy(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Taxonomy(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read taxonomy", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Taxonomy(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
}

// CreateTaxon takes the kind from the path, which the router constrains, never from a form.
func (h *Handler) CreateTaxon(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	kind := r.PathValue("kind")
	f := &TaxonomyForm{
		Slug:    r.PostFormValue("slug"),
		Name:    r.PostFormValue("name"),
		NameEn:  r.PostFormValue("name_en"),
		Parent:  r.PostFormValue("parent"),
		IconKey: r.PostFormValue("icon_key"),
		Tone:    r.PostFormValue("tone"),
		// An unticked box posts nothing, which is the answer "no".
		Comparable: r.PostFormValue("comparable") != "",
	}

	var errs map[string]string
	var err error
	if kind == "categories" {
		errs, err = h.store.CreateCategory(r.Context(), f)
	} else {
		errs, err = h.store.CreateBrand(r.Context(), f)
	}
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create taxon", "error", err, "kind", kind)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		view, readErr := h.store.Taxonomy(r.Context())
		if readErr != nil {
			access.ServerError(w, r, h.log)
			return
		}
		view.Which, view.Errors = kind, errs
		view.Draft = admin.TaxonDraft{
			Slug: f.Slug, Name: f.Name, NameEn: f.NameEn, Parent: f.Parent,
			IconKey: f.IconKey, Tone: f.Tone, Comparable: f.Comparable,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Taxonomy(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
	default:
		http.Redirect(w, r, "/admin/taxonomy?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) EditTaxon(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	kind := "brand"
	if r.PathValue("kind") == "categories" {
		kind = "category"
	}
	slug := r.PathValue("slug")

	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.store.Delete(r.Context(), kind, slug)
	} else {
		err = h.store.Rename(r.Context(), kind, slug,
			r.PostFormValue("name"), r.PostFormValue("name_en"),
			r.PostFormValue("icon_key"), r.PostFormValue("tone"),
			r.PostFormValue("comparable") != "")
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/taxonomy?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInUse):
		http.Redirect(w, r, "/admin/taxonomy?inuse=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/taxonomy?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "edit taxon", "error", err, "kind", kind)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) CorrectDelivery(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	submitted := deliveryFormOf(r.PostFormValue)
	err := h.store.CorrectDelivery(r.Context(), number, submitted)
	if refused, ok := errors.AsType[*DeliveryPostalError](err); ok {
		h.rejectDelivery(w, r, submitted, i18n.T(r.Context(), refused.Key))
		return
	}

	target := "/admin/orders/" + number
	switch {
	case err == nil:
		//nolint:gosec // G710: number is the route's own path value
		http.Redirect(w, r, target+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrTooLateToCorrect):
		//nolint:gosec // G710: same
		http.Redirect(w, r, target+"?toolate=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound):
		//nolint:gosec // G710: same
		http.Redirect(w, r, target+"?deliveryneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "delivery correction refused", "error", err)
		//nolint:gosec // G710: same
		http.Redirect(w, r, target+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "correct delivery", "error", err)
		access.ServerError(w, r, h.log)
	}
}

// rejectDelivery keeps proposed data in the form while the summary continues
// to show the saved destination, so a refusal cannot look like a completed edit.
func (h *Handler) rejectDelivery(w http.ResponseWriter, r *http.Request, d *Delivery, message string) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused delivery correction", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Delivery = admin.Delivery(*d)
	view.DeliveryError = message
	view.AllowanceOperationID = uuid.NewString()
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		admin.Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

const NewsletterIssueLimit = 50

func (h *Handler) Newsletter(w http.ResponseWriter, r *http.Request) {
	view, err := h.newsletterView(r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Newsletter(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
}

// ComposeNewsletter writes a DRAFT and sends nothing: the irreversible step gets its own button.
func (h *Handler) ComposeNewsletter(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	subject, body := r.PostFormValue("subject"), r.PostFormValue("body")

	if keys := newsletter.ValidateIssue(subject, body); len(keys) > 0 {
		view, err := h.newsletterView(r)
		if err != nil {
			h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
			access.ServerError(w, r, h.log)
			return
		}
		view.Draft = admin.NewsletterDraft{Subject: subject, Body: body}
		view.Errors = make(map[string]string, len(keys))
		for field, k := range keys {
			view.Errors[field] = i18n.T(r.Context(), k)
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Newsletter(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
		return
	}

	if _, err := h.letters.Compose(r.Context(), subject, body, staffID(r)); err != nil {
		h.log.ErrorContext(r.Context(), "compose a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/newsletter?saved=1", http.StatusSeeOther)
}

// SendNewsletter relies on the store refusing a second send in the UPDATE's own
// WHERE clause, because a reload is not the only way two of these arrive.
func (h *Handler) SendNewsletter(w http.ResponseWriter, r *http.Request) {
	switch _, err := h.letters.Send(r.Context(), r.PathValue("id"), staffID(r)); {
	case err == nil:
		http.Redirect(w, r, "/admin/newsletter?sent=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrAlreadySent):
		http.Redirect(w, r, "/admin/newsletter?already=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrNoSuchIssue):
		access.NotFound(w, r, h.log)
	default:
		h.log.ErrorContext(r.Context(), "send a newsletter issue", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) newsletterView(r *http.Request) (admin.NewsletterView, error) {
	counts, err := h.letters.Counts(r.Context())
	if err != nil {
		return admin.NewsletterView{}, err
	}
	issues, err := h.letters.Issues(r.Context(), NewsletterIssueLimit)
	if err != nil {
		return admin.NewsletterView{}, err
	}
	view := admin.NewsletterView{
		Active: counts.Active, Unsubscribed: counts.Unsubscribed, Awaiting: counts.Awaiting,
		Issues: make([]admin.NewsletterIssue, 0, len(issues)),
	}
	for i := range issues {
		it := &issues[i]
		view.Issues = append(view.Issues, admin.NewsletterIssue{
			ID: it.ID, Subject: it.Subject, Body: it.Body, Sent: it.Sent,
			SentAt: it.SentAt, Recipients: it.Recipients, SentBy: it.SentBy,
		})
	}
	return view, nil
}

func (h *Handler) IssueInvoice(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.IssueInvoice(r.Context(), number)
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?invoiced=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrAlreadyIssued):
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?hasinvoice=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrDisabled), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "invoice refused", "order", number, "error", err)
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrRejected):
		// The provider's own reason, logged in full because it names the field to
		// fix; the page says one thing, because nobody can act on an RtnCode.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the invoice",
			"order", number, "error", err)
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicefailed=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrPending):
		//nolint:gosec // G710: validated by ordernumber.Valid
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicepending=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "issue invoice", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}

// VoidInvoice voids a wrong invoice so a correct one can be issued in its
// place: a uniform invoice cannot be edited, which invoice_documents_guard enforces.
func (h *Handler) VoidInvoice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	orderPage := "/admin/orders/" + url.PathEscape(number)
	err := h.store.VoidInvoice(r.Context(), number, r.PostFormValue("reason"))
	switch {
	case err == nil:
		http.Redirect(w, r, orderPage+"?voided=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrReason):
		http.Redirect(w, r, orderPage+"?voidreason=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrNotFound):
		http.Redirect(w, r, orderPage+"?noinvoice=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrPending):
		http.Redirect(w, r, orderPage+"?invoicepending=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrRejected):
		// invoicefailed names 統編 and carrier codes. A void form collects a
		// reason; the provider refusal belongs on 綠界, not checkout tax ids.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the void",
			"order", number, "error", err)
		http.Redirect(w, r, orderPage+"?voidfailed=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrDisabled), errors.Is(err, ErrRefused):
		// No issuer is configured. voidfailed would say ECPay refused a call
		// that never happened.
		h.log.WarnContext(r.Context(), "invoice void refused", "order", number, "error", err)
		http.Redirect(w, r, orderPage+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "void invoice", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}

// AllowInvoice files an allowance. A refund leaves the 統一發票 recording a sale that partly did not happen, and
// a 折讓 is the correction the 財政部 accepts for it — a void is for an invoice
// that should not exist, an allowance for one that should exist for less. The
// amount is derived from settled refunds and prior filed allowances inside the
// database claim. The form carries no money for an operator to override.
func (h *Handler) AllowInvoice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	orderPage := "/admin/orders/" + url.PathEscape(number)
	operationID, operationErr := uuid.Parse(r.PostFormValue("operation_id"))
	if operationErr != nil || operationID == uuid.Nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	err := h.store.AllowInvoice(r.Context(), number, operationID)
	switch {
	case err == nil:
		http.Redirect(w, r, orderPage+"?allowed=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrNotFound):
		http.Redirect(w, r, orderPage+"?noinvoice=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrClaimed):
		http.Redirect(w, r, orderPage+"?allowclaimed=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrPending):
		http.Redirect(w, r, orderPage+"?invoicepending=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrTooMuch):
		http.Redirect(w, r, orderPage+"?allowtoomuch=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrRejected):
		// invoicefailed names 統編 and carrier codes, which is right for ISSUING.
		// An allowance form does not collect those fields.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the allowance",
			"order", number, "error", err)
		http.Redirect(w, r, orderPage+"?allowfailed=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrDisabled), errors.Is(err, ErrRefused):
		// No issuer is configured. allowfailed would say ECPay refused a call
		// that never happened.
		h.log.WarnContext(r.Context(), "invoice allowance refused", "order", number, "error", err)
		http.Redirect(w, r, orderPage+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "file invoice allowance", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}
