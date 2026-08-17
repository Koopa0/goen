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

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Handler serves the back office.
type Handler struct {
	// outbox is the queue, for the health page's list of what has given up.
	outbox *outbox.Store
	// images is the media pipeline.
	images *media.Handler
	// stepUp reports whether this session proved a second factor; nil is a
	// deployment with no encryption key, where 2FA is off.
	stepUp func(*http.Request) (bool, error)
	// letters is the mailing list.
	letters *newsletter.Store
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where no session was ever opened.
	sessions SessionCloser
	store    *Store
	log      *slog.Logger
}

// SessionCloser closes a checkout still open at the payment provider.
type SessionCloser interface {
	ExpireSession(ctx context.Context, sessionID string) error
}

// NewHandler returns a Handler over the admin store.
func NewHandler(store *Store, images *media.Handler, messages *outbox.Store,
	letters *newsletter.Store, log *slog.Logger, stepUp func(*http.Request) (bool, error),
	sessions SessionCloser,
) *Handler {
	if store == nil || images == nil || messages == nil || letters == nil || log == nil {
		panic("admin: NewHandler requires a store, a media handler, an outbox, " +
			"a newsletter store and a logger")
	}
	return &Handler{
		store: store, images: images, outbox: messages, letters: letters,
		log: log, stepUp: stepUp, sessions: sessions,
	}
}

// closeSessions expires the checkouts a cancelled order left open at Stripe,
// post-commit and best effort — the stock and the credit are already back.
func (h *Handler) closeSessions(ctx context.Context, number string, sessions []string) {
	if h.sessions == nil {
		return
	}
	for _, id := range sessions {
		if err := h.sessions.ExpireSession(ctx, id); err != nil {
			// Warn, not Error: Stripe refuses to expire anything but an OPEN
			// session, so a checkout completed a moment ago lands here.
			h.log.WarnContext(ctx, "expire checkout session of a cancelled order",
				"order_number", number, "session_id", id, "error", err)
		}
	}
}

// RequireStaff wraps a back-office handler. Signed out and signed-in-but-not-
// staff get the SAME answer, a 404: anything else — a 403, or a redirect to
// /signin?next=/admin — confirms that /admin is a real place.
func (h *Handler) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := account.FromContext(r.Context())
		if !ok || !u.IsAdmin() {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNotFoundTitle)}, "404",
				i18n.T(r.Context(), i18n.KeyAdminNotFoundHead),
				i18n.T(r.Context(), i18n.KeyAdminNotFoundBody)))
			return
		}

		// The SECOND factor, checked here rather than at sign-in: gating the
		// login would need a half-authenticated state to live somewhere, and a
		// session that "does not count yet" eventually counts.
		if h.stepUp != nil {
			verified, err := h.stepUp(r)
			if err != nil {
				h.log.ErrorContext(r.Context(), "read second factor", "error", err)
				h.serverError(w, r)
				return
			}
			if !verified {
				http.Redirect(w, r, "/admin/verify", http.StatusSeeOther)
				return
			}
		}
		next(w, r)
	}
}

// RequireAdmin wraps a back-office handler that changes WHO WORKS HERE: gated
// on the staff predicate, /admin/staff is a self-service promotion desk.
func (h *Handler) RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.RequireStaff(func(w http.ResponseWriter, r *http.Request) {
		u, ok := account.FromContext(r.Context())
		if !ok || !u.IsStaff() {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNotFoundTitle)}, "404",
				i18n.T(r.Context(), i18n.KeyAdminNotFoundHead),
				i18n.T(r.Context(), i18n.KeyAdminNotFoundBody)))
			return
		}
		next(w, r)
	})
}

// Dashboard serves GET /admin.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dashboard", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminDashboard(pages.AdminMeta(r.Context()), view))
}

// Orders serves GET /admin/orders.
func (h *Handler) Orders(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Orders(r.Context(),
		ParseStatus(r.URL.Query().Get("status")), r.URL.Query().Get("q"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read orders", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminOrders(pages.AdminOrdersMeta(r.Context()), view))
}

// Order serves GET /admin/orders/{number}.
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
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK,
		pages.AdminOrder(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

// AdvanceOrder serves POST /admin/orders/{number}/status.
func (h *Handler) AdvanceOrder(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}
	sessions, err := h.store.Advance(r.Context(), number, r.PostFormValue("status"), staffID(r))
	switch {
	case err == nil:
		h.closeSessions(r.Context(), number, sessions)
		http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case errors.Is(err, ErrRefused):
		// Logged in full; the page only says the move was refused, because a
		// constraint name is not something a shop assistant can act on.
		h.log.WarnContext(r.Context(), "order transition refused",
			"order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	default:
		h.log.ErrorContext(r.Context(), "advance order", "error", err)
		h.serverError(w, r)
	}
}

// Ship serves POST /admin/orders/{number}/ship. See [Store.Ship].
func (h *Handler) Ship(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}

	lines, parseErr := parcelLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "dispatch rejected", "order", number, "error", parseErr)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?badparcel=1", http.StatusSeeOther)
		return
	}

	err := h.store.Ship(r.Context(), number, Dispatch{
		Carrier:  r.PostFormValue("carrier"),
		Tracking: r.PostFormValue("tracking"),
		Lines:    lines,
	}, staffID(r))
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?shipped=1", http.StatusSeeOther)
	case errors.Is(err, ErrQuantity):
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?badparcel=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipment refused", "order", number, "error", err)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "ship order", "error", err)
		h.serverError(w, r)
	}
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

// StaffNote serves POST /admin/orders/{number}/note.
func (h *Handler) StaffNote(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}
	if err := h.store.SetStaffNote(r.Context(), number, r.PostFormValue("note")); err != nil {
		h.log.ErrorContext(r.Context(), "set staff note", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
}

// Variants serves GET /admin/stock.
func (h *Handler) Variants(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Variants(r.Context(), r.URL.Query().Get("low") == "1")
	if err != nil {
		h.log.ErrorContext(r.Context(), "read variants", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminVariants(pages.AdminVariantsMeta(r.Context()), view))
}

// AdjustStock serves POST /admin/stock/adjust.
func (h *Handler) AdjustStock(w http.ResponseWriter, r *http.Request) {
	u, _ := account.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	delta, ok := ParseAdjustment(r.PostFormValue("delta"))
	if !ok {
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
		return
	}
	key := r.PostFormValue("idempotency")
	if key == "" {
		key = newKey()
	}

	err := h.store.AdjustStock(r.Context(), r.PostFormValue("sku"), delta, u.ID, key)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/stock?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "stock adjustment refused",
			"sku", r.PostFormValue("sku"), "delta", delta, "error", err)
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "adjust stock", "error", err)
		h.serverError(w, r)
	}
}

// ReceiveStock serves POST /admin/stock/receive, redirecting to the ledger.
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
		h.serverError(w, r)
	}
}

// SetVariantActive serves POST /admin/stock/active.
func (h *Handler) SetVariantActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetVariantActive(r.Context(),
		r.PostFormValue("sku"), r.PostFormValue("active") == "1")
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/stock?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "variant activation refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set variant active", "error", err)
		h.serverError(w, r)
	}
}

// SetVariantPrice serves POST /admin/stock/price.
func (h *Handler) SetVariantPrice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	price, okPrice := ParsePrice(r.PostFormValue("price"))
	compare, okCompare := ParsePrice(r.PostFormValue("compare_at"))
	if !okPrice || !okCompare || price <= 0 {
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
		return
	}

	err := h.store.SetVariantPrice(r.Context(), r.PostFormValue("sku"), price, compare)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/stock?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused), errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "reprice refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set variant price", "error", err)
		h.serverError(w, r)
	}
}

// adminNotices is the one-shot message each redirect parameter carries.
var adminNotices = map[string]i18n.Key{
	"ok":            i18n.KeyAdminNoticeOK,
	"refused":       i18n.KeyAdminNoticeRefused,
	"shipped":       i18n.KeyAdminNoticeShipped,
	"toolate":       i18n.KeyAdminNoticeTooLate,
	"needs":         i18n.KeyAdminNoticeNeeds,
	"toobig":        i18n.KeyAdminNoticeTooBig,
	"notimage":      i18n.KeyAdminNoticeNotImage,
	"uploadfailed":  i18n.KeyAdminNoticeUploadFailed,
	"inuse":         i18n.KeyAdminNoticeInUse,
	"attachrefused": i18n.KeyAdminNoticeAttachRefused,
	"noalt":         i18n.KeyAdminNoticeNoAlt,
	"nodiscount":    i18n.KeyAdminNoticeNoDiscount,
	"refundfailed":  i18n.KeyAdminNoticeRefundFailed,
	"received":      i18n.KeyAdminNoticeReceived,
	"badqty":        i18n.KeyAdminNoticeBadQty,
	"inspected":     i18n.KeyAdminNoticeInspected,
	"closed":        i18n.KeyAdminNoticeClosed,
	"badcount":      i18n.KeyAdminNoticeBadCount,
	"badparcel":     i18n.KeyAdminNoticeBadParcel,
	"invoiced":      i18n.KeyAdminNoticeInvoiced,
	"voided":        i18n.KeyAdminNoticeVoided,
	"hasinvoice":    i18n.KeyAdminNoticeHasInvoice,
	"noinvoice":     i18n.KeyAdminNoticeNoInvoice,
	"invoicefailed": i18n.KeyAdminNoticeInvoiceFailed,
}

// noticeFor turns a redirect's one-shot query parameter into a message.
func noticeFor(r *http.Request) string {
	q := r.URL.Query()
	for name, k := range adminNotices {
		if q.Get(name) == "1" {
			return i18n.T(r.Context(), k)
		}
	}
	return ""
}

// newKey returns an idempotency key for an adjustment whose form lost its own.
func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminErrorTitle)}, "",
		i18n.T(r.Context(), i18n.KeyAdminErrorTitle),
		i18n.T(r.Context(), i18n.KeyAdminErrorBody)))
}

// Returns serves GET /admin/returns.
func (h *Handler) Returns(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Returns(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read return queue", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminReturns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReturns)}, view))
}

// Decide serves POST /admin/returns/{id}/decide. Approving pays money back, so
// a refusal from the database or from Stripe is reported and never swallowed.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.Decide(r.Context(), r.PathValue("id"),
		r.PostFormValue("decision"), r.PostFormValue("resolution"), staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return decision refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		// A Stripe failure lands here, with the refund row already committed as
		// 'failed': a job for a human rather than a lost write.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refundfailed=1", http.StatusSeeOther)
	}
}

// Inspect serves POST /admin/returns/{id}/inspect, one form per parcel.
func (h *Handler) Inspect(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	lines, parseErr := inspectionLines(r)
	if parseErr != nil {
		h.log.WarnContext(r.Context(), "return inspection rejected",
			"return", r.PathValue("id"), "error", parseErr)
		http.Redirect(w, r, "/admin/returns?badcount=1", http.StatusSeeOther)
		return
	}

	err := h.store.InspectReturn(r.Context(), r.PathValue("id"), lines, staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?inspected=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/returns?badcount=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return inspection refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "inspect return",
			"return", r.PathValue("id"), "error", err)
		h.serverError(w, r)
	}
}

// inspectionLines reads the per-line counts off the form, keyed by line id for
// parcelLines' reason: two drifted lists would restock the wrong variant.
func inspectionLines(r *http.Request) ([]ReturnLineInspection, error) {
	var out []ReturnLineInspection
	for name, values := range r.PostForm {
		rest, ok := strings.CutPrefix(name, "received_")
		if !ok || len(values) == 0 {
			continue
		}
		lineID, err := uuid.Parse(rest)
		if err != nil {
			return nil, fmt.Errorf("field %q does not name an order line: %w", name, err)
		}
		received, err := strconv.ParseInt(strings.TrimSpace(values[0]), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("received count on %s: %w", rest, err)
		}
		// Absent means zero: an empty restock box says "none of it", and reading
		// it as "all of it" would put damaged goods back on the shelf.
		var restocked int64
		if raw := strings.TrimSpace(r.PostFormValue("restocked_" + rest)); raw != "" {
			if restocked, err = strconv.ParseInt(raw, 10, 32); err != nil {
				return nil, fmt.Errorf("restocked count on %s: %w", rest, err)
			}
		}
		out = append(out, ReturnLineInspection{
			OrderLineID: lineID,
			Received:    int32(received),
			Restocked:   int32(restocked),
			Note:        strings.TrimSpace(r.PostFormValue("note_" + rest)),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("the form carried no line counts")
	}
	return out, nil
}

// Complete serves POST /admin/returns/{id}/complete.
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.CompleteReturn(r.Context(), r.PathValue("id"),
		r.PostFormValue("resolution"), staffID(r))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/returns?closed=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "return completion refused",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "complete return",
			"return", r.PathValue("id"), "error", err)
		h.serverError(w, r)
	}
}

// Credit serves GET /admin/credit.
func (h *Handler) Credit(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Credit(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read credit ledger", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = creditNotice(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminCredit(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCredit)}, view))
}

// creditNotice is noticeFor plus the BALANCE a grant produced: the form is a
// blank box, so a grant nothing confirms is one somebody makes twice.
func creditNotice(r *http.Request) string {
	if r.URL.Query().Get("ok") != "1" {
		return noticeFor(r)
	}
	balance, err := strconv.ParseInt(r.URL.Query().Get("balance"), 10, 64)
	if err != nil {
		return noticeFor(r)
	}
	return fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminNoticeCreditGranted), pages.TWD(balance))
}

// GrantCredit serves POST /admin/credit. The amount is typed in DOLLARS and
// stored in cents.
func (h *Handler) GrantCredit(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	dollars, parseErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("amount")), 10, 32)
	if parseErr != nil || dollars <= 0 {
		http.Redirect(w, r, "/admin/credit?needs=1", http.StatusSeeOther)
		return
	}

	balance, err := h.store.GrantCredit(r.Context(), r.PostFormValue("email"),
		dollars*100, r.PostFormValue("reason"), staffID(r))
	switch {
	case err == nil:
		// The balance travels as a number and never the address it belongs to,
		// because a query string is logged.
		http.Redirect(w, r, "/admin/credit?ok=1&balance="+
			strconv.FormatInt(balance, 10), http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/credit?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "credit grant refused", "error", err)
		http.Redirect(w, r, "/admin/credit?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "grant credit", "error", err)
		h.serverError(w, r)
	}
}

// Products serves GET /admin/products.
func (h *Handler) Products(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Products(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read products", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProducts(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageProducts)}, view))
}

// NewProduct serves GET /admin/products/new.
func (h *Handler) NewProduct(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.NewProduct(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "new product form", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProductForm(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewProduct)}, view))
}

// CreateProduct serves POST /admin/products.
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := productFormOf(r)
	slug, errs, err := h.store.CreateProduct(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create product", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, true)
	default:
		//nolint:gosec // G710: slug came back from the database
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// EditProduct serves GET /admin/products/{slug}.
func (h *Handler) EditProduct(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Product(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrRefused) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read product", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	if images, imgErr := h.store.ProductImages(r.Context(), r.PathValue("slug")); imgErr != nil {
		// Not fatal: losing the image strip is smaller than losing the page.
		h.log.ErrorContext(r.Context(), "read product images", "error", imgErr)
	} else {
		view.Images = images
	}
	if recent, recentErr := h.images.Recent(r.Context()); recentErr != nil {
		h.log.ErrorContext(r.Context(), "read recent uploads", "error", recentErr)
	} else {
		for _, obj := range recent {
			view.Library = append(view.Library, pages.AdminImage{
				Key: obj.Digest, Width: obj.Width, Height: obj.Height,
			})
		}
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProductForm(
		layouts.Page{Title: view.Name}, view))
}

// UpdateProduct serves POST /admin/products/{slug}.
func (h *Handler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := productFormOf(r)
	f.Slug = r.PathValue("slug") // the slug is the identity; the form cannot move it

	errs, err := h.store.UpdateProduct(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "update product", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, false)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

// PublishProduct serves POST /admin/products/{slug}/status.
func (h *Handler) PublishProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	err := h.store.SetProductStatus(r.Context(), slug, r.PostFormValue("status"))
	if err != nil {
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: validated by the route's own slug
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// AddVariant serves POST /admin/products/{slug}/variants.
func (h *Handler) AddVariant(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	f := &VariantForm{
		SKU:          r.PostFormValue("sku"),
		PriceCents:   dollarsToCents(r.PostFormValue("price")),
		CompareCents: dollarsToCents(r.PostFormValue("compare")),
		SafetyStock:  parseSafetyStock(r.PostFormValue("safety")),
		// Zero is UNMEASURED and stores NULL, so a blank field leaves the variant
		// refused by no shipping method rather than blocked from all of them.
		ParcelLongestMM: parseSafetyStock(r.PostFormValue("parcel_longest")),
		ParcelSumMM:     parseSafetyStock(r.PostFormValue("parcel_sum")),
		ParcelWeightG:   parseSafetyStock(r.PostFormValue("parcel_weight")),
		// One select per option, all named option_value; PostForm holds them in
		// the order the page rendered the axes.
		OptionValues: r.PostForm["option_value"],
	}
	errs, err := h.store.AddVariant(r.Context(), slug, f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// productFormOf reads the product form off a request.
func productFormOf(r *http.Request) *ProductForm {
	return &ProductForm{
		Slug:           r.PostFormValue("slug"),
		Name:           r.PostFormValue("name"),
		Summary:        r.PostFormValue("summary"),
		Description:    r.PostFormValue("description"),
		NameEn:         r.PostFormValue("name_en"),
		SummaryEn:      r.PostFormValue("summary_en"),
		DescriptionEn:  r.PostFormValue("description_en"),
		WarrantyNote:   r.PostFormValue("warranty"),
		WarrantyMonths: parseSafetyStock(r.PostFormValue("warranty_months")),
		BrandID:        r.PostFormValue("brand"),
		CategoryID:     r.PostFormValue("category"),
	}
}

// rejectProduct re-renders the form at 422 with what was typed still in it.
func (h *Handler) rejectProduct(w http.ResponseWriter, r *http.Request, f *ProductForm, errs map[string]string, isNew bool) {
	var view pages.AdminProductView
	var err error
	if isNew {
		view, err = h.store.NewProduct(r.Context())
	} else {
		view, err = h.store.Product(r.Context(), f.Slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "rebuild product form", "error", err)
		h.serverError(w, r)
		return
	}
	view.Slug, view.Name, view.Summary = f.Slug, f.Name, f.Summary
	view.Description, view.WarrantyNote = f.Description, f.WarrantyNote
	view.WarrantyMonths = f.WarrantyMonths
	view.NameEn, view.SummaryEn = f.NameEn, f.SummaryEn
	view.DescriptionEn = f.DescriptionEn
	view.BrandID, view.CategoryID = f.BrandID, f.CategoryID
	view.Errors = errs
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
}

// dollarsToCents reads a price typed in whole New Taiwan DOLLARS.
func dollarsToCents(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 || n > MaxPriceCents/100 {
		return 0
	}
	return n * 100
}

// parseSafetyStock reads the safety-stock field, returning int32 directly
// because gosec cannot see that a caller-side ceiling makes the conversion safe.
func parseSafetyStock(s string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n < 0 || n > 1_000_000 {
		return 0
	}
	return int32(n)
}

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNotFoundTitle)}, "404",
		i18n.T(r.Context(), i18n.KeyAdminNotFoundHead),
		i18n.T(r.Context(), i18n.KeyAdminNotFoundBody)))
}

// Coupons serves GET /admin/coupons.
func (h *Handler) Coupons(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Coupons(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read coupons", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminCoupons(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCoupons)}, view))
}

// CreateCoupon serves POST /admin/coupons.
func (h *Handler) CreateCoupon(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &CouponForm{
		Code:            r.PostFormValue("code"),
		Description:     r.PostFormValue("description"),
		Kind:            r.PostFormValue("kind"),
		Value:           whole(r.PostFormValue("value")),
		CapDollars:      whole(r.PostFormValue("cap")),
		MinSpendDollars: whole(r.PostFormValue("min")),
		MaxRedemptions:  small(r.PostFormValue("max")),
		PerCustomer:     small(r.PostFormValue("percustomer")),
		Days:            small(r.PostFormValue("days")),
	}
	// An unfilled per-customer box means the schema's own default, not zero.
	if f.PerCustomer == 0 && strings.TrimSpace(r.PostFormValue("percustomer")) == "" {
		f.PerCustomer = 1
	}

	errs, err := h.store.CreateCoupon(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create coupon", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		view, readErr := h.store.Coupons(r.Context())
		if readErr != nil {
			h.serverError(w, r)
			return
		}
		view.Errors = errs
		view.Draft = pages.AdminCouponDraft{
			Code: f.Code, Description: f.Description, Kind: f.Kind,
			Value:       r.PostFormValue("value"),
			Cap:         r.PostFormValue("cap"),
			MinSpend:    r.PostFormValue("min"),
			MaxRedeem:   r.PostFormValue("max"),
			PerCustomer: r.PostFormValue("percustomer"),
			Days:        r.PostFormValue("days"),
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminCoupons(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCoupons)}, view))
	default:
		http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
	}
}

// SetCouponActive serves POST /admin/coupons/{code}/active.
func (h *Handler) SetCouponActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if err := h.store.SetCouponActive(r.Context(), r.PathValue("code"),
		r.PostFormValue("active") == "true"); err != nil {
		h.log.WarnContext(r.Context(), "set coupon active", "error", err)
		http.Redirect(w, r, "/admin/coupons?refused=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
}

// whole reads a figure typed in whole units — dollars, or percent.
func whole(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 || n > MaxPriceCents/100 {
		return 0
	}
	return n
}

// small reads a count bounded well below int32, so no conversion overflows.
func small(s string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n < 0 || n > 1_000_000 {
		return 0
	}
	return int32(n)
}

// Campaigns serves GET /admin/campaigns.
func (h *Handler) Campaigns(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Campaigns(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaigns", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminCampaigns(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
}

// CreateCampaign serves POST /admin/campaigns.
func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &CampaignForm{
		Slug:  r.PostFormValue("slug"),
		Title: r.PostFormValue("title"),
		Days:  small(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateCampaign(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create campaign", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		view, readErr := h.store.Campaigns(r.Context())
		if readErr != nil {
			h.serverError(w, r)
			return
		}
		view.Errors = errs
		view.Draft = pages.AdminCampaignDraft{
			Slug: f.Slug, Title: f.Title, Days: r.PostFormValue("days"),
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminCampaigns(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCampaigns)}, view))
	default:
		//nolint:gosec // G710: slug matched slugFormat in Validate
		http.Redirect(w, r, "/admin/campaigns/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

// EditCampaign serves GET /admin/campaigns/{slug}.
func (h *Handler) EditCampaign(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	products, err := h.store.CampaignProducts(r.Context(), slug)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read campaign products", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminCampaignForm(
		layouts.Page{Title: slug}, pages.AdminCampaignView{
			Slug: slug, Products: products, Notice: noticeFor(r),
		}))
}

// FeatureProduct serves POST /admin/campaigns/{slug}/products.
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
		// Usually sale_campaign_needs_discount: nothing is marked down.
		h.log.WarnContext(r.Context(), "feature product", "campaign", slug, "error", err)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/campaigns/"+slug+"?nodiscount=1", http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/campaigns/"+slug+"?ok=1", http.StatusSeeOther)
}

// SetCampaignActive serves POST /admin/campaigns/{slug}/active.
func (h *Handler) SetCampaignActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if err := h.store.SetCampaignActive(r.Context(), r.PathValue("slug"),
		r.PostFormValue("active") == "true"); err != nil {
		h.log.WarnContext(r.Context(), "set campaign active", "error", err)
		http.Redirect(w, r, "/admin/campaigns?refused=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/campaigns?ok=1", http.StatusSeeOther)
}

// Audit serves GET /admin/audit.
func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Audit(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read audit", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminAudit(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageAudit)}, view))
}

// UploadImage serves POST /admin/products/{slug}/images. Store then attach,
// deliberately NOT one transaction: storing is idempotent by content, so a
// failure between them leaves only an orphan UnreferencedMedia reclaims.
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.ReadUpload(w, r, "image")
	if err != nil {
		h.log.WarnContext(r.Context(), "image upload", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+uploadReason(err), http.StatusSeeOther)
		return
	}

	alt := r.PostFormValue("alt")
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest, alt,
		r.PostFormValue("alt_en"), obj.Width, obj.Height); err != nil {
		h.log.WarnContext(r.Context(), "attach image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// ReuseImage serves POST /admin/products/{slug}/images/reuse, attaching an
// image ALREADY uploaded to a second product.
func (h *Handler) ReuseImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

	// The dimensions come from the STORED object and not the form: the srcset is
	// built from them, so a hand-edited pair lays out against the wrong size.
	obj, err := h.images.Object(r.Context(), r.PostFormValue("digest"))
	if err != nil {
		h.log.WarnContext(r.Context(), "reuse image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?needs=1", http.StatusSeeOther)
		return
	}
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest,
		r.PostFormValue("alt"), r.PostFormValue("alt_en"),
		obj.Width, obj.Height); err != nil {
		h.log.WarnContext(r.Context(), "attach reused image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// RemoveImage serves POST /admin/products/{slug}/images/remove.
func (h *Handler) RemoveImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.DetachImage(r.Context(), slug, r.PostFormValue("digest")); err != nil {
		h.log.WarnContext(r.Context(), "detach image", "error", err, "slug", slug)
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// Movements serves GET /admin/stock/{sku}.
func (h *Handler) Movements(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Movements(r.Context(), r.PathValue("sku"))
	switch {
	case err == nil:
		view.Notice = noticeFor(r)
		web.Render(w, r, h.log, http.StatusOK, pages.AdminMovements(
			layouts.Page{Title: view.SKU}, &view))
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
	default:
		h.log.ErrorContext(r.Context(), "read movements", "error", err)
		h.serverError(w, r)
	}
}

// CreateShippingMethod serves POST /admin/shipping/method.
func (h *Handler) CreateShippingMethod(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	m := &NewMethod{
		Code:        r.PostFormValue("code"),
		Destination: r.PostFormValue("destination"),
		// Zero is "no stated limit", the honest default for home delivery.
		MaxParcelLongestMM: parseSafetyStock(r.PostFormValue("max_parcel_longest")),
		MaxParcelSumMM:     parseSafetyStock(r.PostFormValue("max_parcel_sum")),
		MaxParcelWeightG:   parseSafetyStock(r.PostFormValue("max_parcel_weight")),
		Name:               r.PostFormValue("name"),
		NameEn:             r.PostFormValue("name_en"),
		Carrier:            r.PostFormValue("carrier"),
		CarrierEn:          r.PostFormValue("carrier_en"),
		FeeDollars:         dollars(r.PostFormValue("fee")),
		FreeOverDollars:    dollars(r.PostFormValue("free_over")),
	}
	errs, err := h.store.CreateMethod(r.Context(), m)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create shipping method", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{
			Code: m.Code, Destination: m.Destination,
			Name: m.Name, NameEn: m.NameEn,
			Carrier: m.Carrier, CarrierEn: m.CarrierEn,
			Fee: r.PostFormValue("fee"), FreeOver: r.PostFormValue("free_over"),
		}, pages.AdminZoneDraft{})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// SetShippingMethodActive serves POST /admin/shipping/method/{id}/active. A
// method is switched OFF, never deleted: past orders name their version.
func (h *Handler) SetShippingMethodActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetMethodActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "1")
	if err != nil {
		h.log.WarnContext(r.Context(), "toggle shipping method", "error", err)
		h.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
}

// CreateShippingZone serves POST /admin/shipping/zone.
func (h *Handler) CreateShippingZone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	z := &NewZone{
		Code:     r.PostFormValue("code"),
		Name:     r.PostFormValue("name"),
		NameEn:   r.PostFormValue("name_en"),
		Prefixes: r.PostFormValue("prefixes"),
	}
	errs, err := h.store.CreateZone(r.Context(), z)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create shipping zone", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{}, pages.AdminZoneDraft{
			Code: z.Code, Name: z.Name, NameEn: z.NameEn, Prefixes: z.Prefixes,
		})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// SetZonePrefixes serves POST /admin/shipping/zone/{id}/prefixes.
func (h *Handler) SetZonePrefixes(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	errs, err := h.store.SetZonePrefixes(r.Context(), r.PathValue("id"),
		r.PostFormValue("prefixes"))
	switch {
	case err != nil:
		h.log.WarnContext(r.Context(), "set zone prefixes", "error", err)
		h.notFound(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{}, pages.AdminZoneDraft{})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// DeleteShippingZone serves POST /admin/shipping/zone/{id}/delete.
func (h *Handler) DeleteShippingZone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.DeleteZone(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInUse):
		http.Redirect(w, r, "/admin/shipping?inuse=1", http.StatusSeeOther)
	default:
		h.log.WarnContext(r.Context(), "delete shipping zone", "error", err)
		h.notFound(w, r)
	}
}

// rejectShippingForm re-renders /admin/shipping at 422 with what was typed in it.
func (h *Handler) rejectShippingForm(
	w http.ResponseWriter, r *http.Request, errs map[string]string,
	method pages.AdminMethodDraft, zone pages.AdminZoneDraft,
) {
	view, err := h.store.Shipping(r.Context())
	if err != nil {
		h.serverError(w, r)
		return
	}
	view.Errors, view.MethodDraft, view.ZoneDraft = errs, method, zone
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminShipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

// dollars reads a whole-dollar amount; a blank or unparseable box is zero,
// which NewMethod.Validate reads per field.
func dollars(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// FAQ serves GET /admin/faq.
func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminFAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

// CreateFAQEntry serves POST /admin/faq.
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
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

// EditFAQEntry serves POST /admin/faq/{id}: save or delete, by submitted button.
func (h *Handler) EditFAQEntry(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if r.PostFormValue("action") == "delete" {
		if err := h.store.DeleteFAQEntry(r.Context(), id); err != nil {
			h.log.WarnContext(r.Context(), "delete faq entry", "error", err)
			h.notFound(w, r)
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
		h.notFound(w, r)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update faq entry", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectFAQ(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/faq?ok=1", http.StatusSeeOther)
	}
}

// faqFormOf reads the FAQ form off a request.
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

// rejectFAQ re-renders the page at 422 with what was typed still in it.
func (h *Handler) rejectFAQ(
	w http.ResponseWriter, r *http.Request, f *FAQForm, errs map[string]string,
) {
	view, err := h.store.FAQ(r.Context())
	if err != nil {
		h.serverError(w, r)
		return
	}
	view.Errors = errs
	view.Draft = pages.AdminFAQEntry{
		Category: f.Category, Question: f.Question, Answer: f.Answer,
		CategoryEn: f.CategoryEn, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminFAQ(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageFAQ)}, &view))
}

// AddOption serves POST /admin/products/{slug}/options.
func (h *Handler) AddOption(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOption(r.Context(), slug, OptionDraft{
			Name:   r.PostFormValue("name"),
			NameEn: r.PostFormValue("name_en"),
		})
	})
}

// AddOptionValue serves POST /admin/products/{slug}/options/values.
func (h *Handler) AddOptionValue(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOptionValue(r.Context(), slug, OptionDraft{
			OptionID: r.PostFormValue("option"),
			Name:     r.PostFormValue("value"),
			NameEn:   r.PostFormValue("value_en"),
		})
	})
}

// optionWrite is the shape both option forms share.
func (h *Handler) optionWrite(
	w http.ResponseWriter, r *http.Request, write func(slug string) (map[string]string, error),
) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	errs, err := write(slug)
	switch {
	case err != nil:
		h.log.WarnContext(r.Context(), "write product option", "error", err, "slug", slug)
		h.notFound(w, r)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs)
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// AddSpec serves POST /admin/products/{slug}/specs.
func (h *Handler) AddSpec(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	errs, err := h.store.AddSpec(r.Context(), slug, SpecDraft{
		Label:   r.PostFormValue("label"),
		Value:   r.PostFormValue("value"),
		LabelEn: r.PostFormValue("label_en"),
		ValueEn: r.PostFormValue("value_en"),
	})
	switch {
	case err != nil:
		h.log.WarnContext(r.Context(), "add spec", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?specfailed=1", http.StatusSeeOther)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs)
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// RemoveSpec serves POST /admin/products/{slug}/specs/remove.
func (h *Handler) RemoveSpec(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.RemoveSpec(r.Context(), slug, r.PostFormValue("spec")); err != nil {
		h.log.WarnContext(r.Context(), "remove spec", "error", err, "slug", slug)
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// editProductWithErrors re-renders the edit page at 422 with the refusals on it.
func (h *Handler) editProductWithErrors(
	w http.ResponseWriter, r *http.Request, slug string, errs map[string]string,
) {
	view, err := h.store.Product(r.Context(), slug)
	if err != nil {
		h.notFound(w, r)
		return
	}
	view.Errors = errs
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
}

// attachReason turns an attach failure into the query the page reads.
func attachReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "noalt=1"
	case errors.Is(err, ErrRefused):
		return "attachrefused=1"
	default:
		return "uploadfailed=1"
	}
}

// uploadReason turns a rejection into the query the page reads. Naming WHICH
// decoder refused a file would tell an attacker which decoders are wired up.
func uploadReason(err error) string {
	switch {
	case errors.Is(err, media.ErrTooLarge):
		return "toobig=1"
	case errors.Is(err, media.ErrNotAnImage):
		return "notimage=1"
	default:
		return "uploadfailed=1"
	}
}

// HomeContent serves GET /admin/home.
func (h *Handler) HomeContent(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read hero slides", "error", err)
		h.serverError(w, r)
		return
	}
	banners, err := h.store.Banners(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read promo banners", "error", err)
		h.serverError(w, r)
		return
	}
	view.Banners = banners
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminHome(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

// CreateBanner serves POST /admin/home/banner.
func (h *Handler) CreateBanner(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := &BannerForm{
		Message:  r.PostFormValue("message"),
		Short:    r.PostFormValue("short"),
		Code:     r.PostFormValue("code"),
		CTALabel: r.PostFormValue("cta_label"),
		CTAHref:  r.PostFormValue("cta_href"),
		// The English strip, all optional.
		MessageEn:  r.PostFormValue("message_en"),
		ShortEn:    r.PostFormValue("short_en"),
		CTALabelEn: r.PostFormValue("cta_label_en"),
		Days:       small(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateBanner(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create promo banner", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectBanner(w, r, f, errs)
	default:
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

// SetBannerActive serves POST /admin/home/banner/{id}/active.
func (h *Handler) SetBannerActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetBannerActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "1")
	if err != nil {
		h.log.WarnContext(r.Context(), "toggle promo banner", "error", err)
		h.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
}

// rejectBanner re-renders the page at 422 with what was typed still in it.
func (h *Handler) rejectBanner(
	w http.ResponseWriter, r *http.Request, f *BannerForm, errs map[string]string,
) {
	view, err := h.store.HeroSlides(r.Context())
	if err != nil {
		h.serverError(w, r)
		return
	}
	if banners, bannerErr := h.store.Banners(r.Context()); bannerErr == nil {
		view.Banners = banners
	}
	view.Errors = errs
	view.BannerDraft = pages.AdminBannerDraft{
		Message: f.Message, Short: f.Short, Code: f.Code,
		CTALabel: f.CTALabel, CTAHref: f.CTAHref, Days: r.PostFormValue("days"),
		MessageEn: f.MessageEn, ShortEn: f.ShortEn, CTALabelEn: f.CTALabelEn,
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminHome(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
}

// CreateHeroSlide serves POST /admin/home. Multipart, because the artwork
// arrives with the copy; the image is optional and a slide with none falls back
// to the built-in artwork.
func (h *Handler) CreateHeroSlide(w http.ResponseWriter, r *http.Request) {
	obj, err := h.images.ReadUpload(w, r, "image")
	if err != nil && !errors.Is(err, media.ErrNotAnImage) {
		h.log.WarnContext(r.Context(), "hero image", "error", err)
		http.Redirect(w, r, "/admin/home?"+uploadReason(err), http.StatusSeeOther)
		return
	}

	f := &HeroForm{
		Eyebrow:      r.PostFormValue("eyebrow"),
		Headline:     r.PostFormValue("headline"),
		Body:         r.PostFormValue("body"),
		PrimaryLabel: r.PostFormValue("primary_label"),
		PrimaryHref:  r.PostFormValue("primary_href"),
		SecondLabel:  r.PostFormValue("second_label"),
		SecondHref:   r.PostFormValue("second_href"),
		ImageKey:     obj.Digest,
		ImageAlt:     r.PostFormValue("alt"),
		// The English hero, all optional.
		EyebrowEn:      r.PostFormValue("eyebrow_en"),
		HeadlineEn:     r.PostFormValue("headline_en"),
		BodyEn:         r.PostFormValue("body_en"),
		PrimaryLabelEn: r.PostFormValue("primary_label_en"),
		SecondLabelEn:  r.PostFormValue("second_label_en"),
		ImageAltEn:     r.PostFormValue("alt_en"),
		Days:           small(r.PostFormValue("days")),
	}
	errs, err := h.store.CreateHeroSlide(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create hero slide", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		view, readErr := h.store.HeroSlides(r.Context())
		if readErr != nil {
			h.serverError(w, r)
			return
		}
		view.Errors = errs
		view.Draft = pages.AdminHeroDraft{
			Eyebrow: f.Eyebrow, Headline: f.Headline, Body: f.Body,
			PrimaryLabel: f.PrimaryLabel, PrimaryHref: r.PostFormValue("primary_href"),
			SecondLabel: f.SecondLabel, SecondHref: r.PostFormValue("second_href"),
			ImageKey: f.ImageKey, ImageAlt: f.ImageAlt, Days: r.PostFormValue("days"),
			EyebrowEn: f.EyebrowEn, HeadlineEn: f.HeadlineEn, BodyEn: f.BodyEn,
			PrimaryLabelEn: f.PrimaryLabelEn, SecondLabelEn: f.SecondLabelEn,
			ImageAltEn: f.ImageAltEn,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminHome(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHero)}, &view))
	default:
		http.Redirect(w, r, "/admin/home?ok=1", http.StatusSeeOther)
	}
}

// SetHeroSlideActive serves POST /admin/home/{id}/active.
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

// PromoteHeroSlide serves POST /admin/home/{id}/promote.
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

// Taxonomy serves GET /admin/taxonomy.
func (h *Handler) Taxonomy(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Taxonomy(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read taxonomy", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminTaxonomy(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
}

// CreateTaxon serves POST /admin/taxonomy/{kind}. The kind is a path value the
// router constrains, never anything a form supplies.
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
		h.serverError(w, r)
	case len(errs) > 0:
		view, readErr := h.store.Taxonomy(r.Context())
		if readErr != nil {
			h.serverError(w, r)
			return
		}
		view.Which, view.Errors = kind, errs
		view.Draft = pages.AdminTaxonDraft{
			Slug: f.Slug, Name: f.Name, NameEn: f.NameEn, Parent: f.Parent,
			IconKey: f.IconKey,
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminTaxonomy(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTaxonomy)}, &view))
	default:
		http.Redirect(w, r, "/admin/taxonomy?ok=1", http.StatusSeeOther)
	}
}

// EditTaxon serves POST /admin/taxonomy/{kind}/{slug}: rename or delete.
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
			r.PostFormValue("icon_key"))
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
		h.serverError(w, r)
	}
}

// Reports serves GET /admin/reports.
func (h *Handler) Reports(w http.ResponseWriter, r *http.Request) {
	// A parse failure is zero, which the store's allowlist turns into the
	// default — the same answer an out-of-range number gets.
	days, parseErr := strconv.ParseInt(r.URL.Query().Get("days"), 10, 32)
	if parseErr != nil {
		days = 0
	}
	view, err := h.store.Report(r.Context(), int32(days))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read report", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminReport(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReports)}, &view))
}

// Questions serves GET /admin/questions.
func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Questions(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read questions", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminQuestions(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageQuestions)}, view))
}

// AnswerQuestion serves POST /admin/questions/{id}: answer or hide.
func (h *Handler) AnswerQuestion(w http.ResponseWriter, r *http.Request) {
	u, ok := account.FromContext(r.Context())
	if !ok {
		h.notFound(w, r)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")

	var err error
	if r.PostFormValue("action") == "hide" {
		err = h.store.HideQuestion(r.Context(), id)
	} else {
		// staff=true, because this endpoint IS the shop; product_answers.is_staff
		// is stored with the answer rather than re-derived later.
		err = h.store.AnswerQuestion(r.Context(), id, u.ID, r.PostFormValue("body"))
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/questions?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/questions?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "answer question", "error", err)
		h.serverError(w, r)
	}
}

// Health serves GET /admin/health.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.WorkerHealth(r.Context(), h.outbox)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read worker health", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminHealth(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHealth)}, view))
}

// Shipping serves GET /admin/shipping.
func (h *Handler) Shipping(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Shipping(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping configuration", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminShipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

// PublishShippingVersion serves POST /admin/shipping/version.
func (h *Handler) PublishShippingVersion(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	fee, feeErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("fee")), 10, 64)
	if feeErr != nil {
		http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
		return
	}
	// An empty threshold is "no free shipping" and not zero, and ParseInt
	// refuses "" rather than answering 0.
	var freeOver int64
	if raw := strings.TrimSpace(r.PostFormValue("free_over")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
			return
		}
		freeOver = parsed
	}

	err := h.store.PublishShippingVersion(r.Context(), ShippingVersion{
		MethodID: r.PostFormValue("method"),
		Name:     r.PostFormValue("name"),
		Carrier:  r.PostFormValue("carrier"),
		// Optional; the checkout's chooser reads them.
		NameEn:          r.PostFormValue("name_en"),
		CarrierEn:       r.PostFormValue("carrier_en"),
		FeeDollars:      fee,
		FreeOverDollars: freeOver,
	})
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

// SetZoneSurcharge serves POST /admin/shipping/surcharge.
func (h *Handler) SetZoneSurcharge(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	// An empty box means zero here, which CLEARS the surcharge: the field renders
	// blank when there is none, so submitting it untouched must be a no-op.
	var amount int64
	if raw := strings.TrimSpace(r.PostFormValue("amount")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
			return
		}
		amount = parsed
	}

	err := h.store.SetZoneSurcharge(r.Context(), r.PostFormValue("version"),
		r.PostFormValue("zone"), amount)
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

// redirectShipping turns a store error into the page's own answer.
func (h *Handler) redirectShipping(w http.ResponseWriter, r *http.Request, err error, ok string) {
	switch {
	case err == nil:
		http.Redirect(w, r, ok, http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipping change refused", "error", err)
		http.Redirect(w, r, "/admin/shipping?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change shipping", "error", err)
		h.serverError(w, r)
	}
}

// Tiers serves GET /admin/tiers.
func (h *Handler) Tiers(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Tiers(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read membership tiers", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminTiers(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageTiers)}, view))
}

// CreateTier serves POST /admin/tiers.
func (h *Handler) CreateTier(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	threshold, tErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("threshold")), 10, 64)
	percent, pErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("percent")), 10, 64)
	if tErr != nil || pErr != nil {
		http.Redirect(w, r, "/admin/tiers?needs=1", http.StatusSeeOther)
		return
	}
	err := h.store.CreateTier(r.Context(), r.PostFormValue("code"),
		r.PostFormValue("name"), r.PostFormValue("name_en"), threshold, percent)
	h.redirectTiers(w, r, err)
}

// DeleteTier serves POST /admin/tiers/delete.
func (h *Handler) DeleteTier(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirectTiers(w, r, h.store.DeleteTier(r.Context(), r.PostFormValue("tier")))
}

// redirectTiers turns a store error into the page's own answer.
func (h *Handler) redirectTiers(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/tiers?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/tiers?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "tier change refused", "error", err)
		http.Redirect(w, r, "/admin/tiers?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change membership tiers", "error", err)
		h.serverError(w, r)
	}
}

// CorrectDelivery serves POST /admin/orders/{number}/delivery.
func (h *Handler) CorrectDelivery(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	err := h.store.CorrectDelivery(r.Context(), number, deliveryFormOf(r.PostFormValue))

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
		http.Redirect(w, r, target+"?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "delivery correction refused", "error", err)
		//nolint:gosec // G710: same
		http.Redirect(w, r, target+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "correct delivery", "error", err)
		h.serverError(w, r)
	}
}

// Reviews serves GET /admin/reviews.
func (h *Handler) Reviews(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Reviews(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read reviews", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminReviews(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageReviews)}, view))
}

// HideReview serves POST /admin/reviews/hide.
func (h *Handler) HideReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, true)
}

// ShowReview serves POST /admin/reviews/show.
func (h *Handler) ShowReview(w http.ResponseWriter, r *http.Request) {
	h.setReviewHidden(w, r, false)
}

func (h *Handler) setReviewHidden(w http.ResponseWriter, r *http.Request, hidden bool) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetReviewHidden(r.Context(), r.PostFormValue("review"), hidden); {
	case err == nil:
		http.Redirect(w, r, "/admin/reviews?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/reviews", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set review hidden", "error", err)
		h.serverError(w, r)
	}
}

// Messages serves GET /admin/messages.
func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Messages(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read contact messages", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminMessages(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageMessages)}, view))
}

// HandleMessage serves POST /admin/messages/handle.
func (h *Handler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, true)
}

// ReopenMessage serves POST /admin/messages/reopen.
func (h *Handler) ReopenMessage(w http.ResponseWriter, r *http.Request) {
	h.setMessageHandled(w, r, false)
}

func (h *Handler) setMessageHandled(w http.ResponseWriter, r *http.Request, handled bool) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	switch err := h.store.SetMessageHandled(r.Context(), r.PostFormValue("message"), handled); {
	case err == nil:
		http.Redirect(w, r, "/admin/messages?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/admin/messages", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set message handled", "error", err)
		h.serverError(w, r)
	}
}

// NewsletterIssueLimit bounds the issue list.
const NewsletterIssueLimit = 50

// Newsletter serves GET /admin/newsletter.
func (h *Handler) Newsletter(w http.ResponseWriter, r *http.Request) {
	view, err := h.newsletterView(r)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read the newsletter", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminNewsletter(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
}

// ComposeNewsletter serves POST /admin/newsletter. It writes a DRAFT and sends
// nothing: the irreversible step gets its own button.
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
			h.serverError(w, r)
			return
		}
		view.Draft = pages.AdminNewsletterDraft{Subject: subject, Body: body}
		view.Errors = make(map[string]string, len(keys))
		for field, k := range keys {
			view.Errors[field] = i18n.T(r.Context(), k)
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminNewsletter(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewsletter)}, view))
		return
	}

	if _, err := h.letters.Compose(r.Context(), subject, body); err != nil {
		h.log.ErrorContext(r.Context(), "compose a newsletter issue", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/admin/newsletter?saved=1", http.StatusSeeOther)
}

// SendNewsletter serves POST /admin/newsletter/{id}/send. The store refuses a
// second send in the UPDATE's own WHERE clause, because a reload is not the
// only way two of these arrive.
func (h *Handler) SendNewsletter(w http.ResponseWriter, r *http.Request) {
	switch _, err := h.letters.Send(r.Context(), r.PathValue("id"), staffID(r)); {
	case err == nil:
		http.Redirect(w, r, "/admin/newsletter?sent=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrAlreadySent):
		http.Redirect(w, r, "/admin/newsletter?already=1", http.StatusSeeOther)
	case errors.Is(err, newsletter.ErrNoSuchIssue):
		h.notFound(w, r)
	default:
		h.log.ErrorContext(r.Context(), "send a newsletter issue", "error", err)
		h.serverError(w, r)
	}
}

// newsletterView reads the counts and the issues together.
func (h *Handler) newsletterView(r *http.Request) (pages.AdminNewsletterView, error) {
	counts, err := h.letters.Counts(r.Context())
	if err != nil {
		return pages.AdminNewsletterView{}, err
	}
	issues, err := h.letters.Issues(r.Context(), NewsletterIssueLimit)
	if err != nil {
		return pages.AdminNewsletterView{}, err
	}
	view := pages.AdminNewsletterView{
		Active: counts.Active, Unsubscribed: counts.Unsubscribed, Awaiting: counts.Awaiting,
		Issues: make([]pages.AdminNewsletterIssue, 0, len(issues)),
	}
	for i := range issues {
		it := &issues[i]
		view.Issues = append(view.Issues, pages.AdminNewsletterIssue{
			ID: it.ID, Subject: it.Subject, Body: it.Body, Sent: it.Sent,
			SentAt: it.SentAt, Recipients: it.Recipients, SentBy: it.SentBy,
		})
	}
	return view, nil
}

// Customers serves GET /admin/customers.
func (h *Handler) Customers(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Customers(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "search customers", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminCustomers(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCustomers)}, view))
}

// Warranties serves GET /admin/warranty, the shop's half of registration.
func (h *Handler) Warranties(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Warranties(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "search warranties", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminWarranties(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageWarranty)}, view))
}

// Customer serves GET /admin/customers/{id}.
func (h *Handler) Customer(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Customer(r.Context(), r.PathValue("id"), staffID(r))
	switch {
	case err == nil:
		web.Render(w, r, h.log, http.StatusOK, pages.AdminCustomer(
			layouts.Page{Title: view.DisplayName()}, &view))
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
	default:
		h.log.ErrorContext(r.Context(), "read customer", "error", err)
		h.serverError(w, r)
	}
}

// IssueInvoice serves POST /admin/orders/{number}/invoice, acting on the
// invoice preference collected at checkout.
func (h *Handler) IssueInvoice(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.IssueInvoice(r.Context(), number)
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoiced=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrAlreadyIssued):
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?hasinvoice=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrDisabled), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "invoice refused", "order", number, "error", err)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrRejected):
		// The provider's own reason, logged in full because it names the field to
		// fix; the page says one thing, because nobody can act on an RtnCode.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the invoice",
			"order", number, "error", err)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicefailed=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "issue invoice", "order", number, "error", err)
		h.serverError(w, r)
	}
}

// VoidInvoice serves POST /admin/orders/{number}/invoice/void. A uniform
// invoice cannot be edited: a wrong one is voided and a correct one issued in
// its place, which invoice_documents_guard enforces.
func (h *Handler) VoidInvoice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !IsOrderNumber(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.VoidInvoice(r.Context(), number, r.PostFormValue("reason"))
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?voided=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrNotFound):
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?noinvoice=1", http.StatusSeeOther)
	case errors.Is(err, invoice.ErrRejected), errors.Is(err, invoice.ErrDisabled),
		errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "invoice void refused", "order", number, "error", err)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicefailed=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "void invoice", "order", number, "error", err)
		h.serverError(w, r)
	}
}
