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
	// Held by the handler for the same reason images is: the store's job is
	// this feature's own tables.
	outbox *outbox.Store
	// images is the media pipeline. Held by the handler and not the store,
	// because reading a multipart body is an HTTP concern — the store deals in
	// a digest that already exists.
	images *media.Handler
	// stepUp reports whether this request's session has proved a second factor.
	//
	// A function and not a *twofactor.Store, because internal/admin needs one
	// answer out of a package that does a great deal more — and because a
	// deployment without an encryption key passes nil, which is what makes 2FA
	// optional without a boolean flag threaded through every call.
	stepUp func(*http.Request) (bool, error)
	// letters is the mailing list. Held by the handler rather than reached through
	// the admin store for the same reason outbox and images are: the store's job
	// is this feature's own tables, and newsletter owns its own.
	letters *newsletter.Store
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where no session was ever opened.
	sessions SessionCloser
	store    *Store
	log      *slog.Logger
}

// SessionCloser closes a checkout the customer may still have open at the
// payment provider.
//
// Defined here rather than imported, the same one-method seam internal/cart
// defines for the same *payment.Gateway. Two consumers naming one method is the
// rule this repository already follows for order access; importing internal/
// payment from the back office to share a type would couple the two features for
// one signature.
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

// closeSessions expires the checkouts a cancelled order left open at Stripe.
//
// The customer's own cancel does this too, and it has to be done from BOTH
// doors for the reason the store-credit reversal is: the shop cancelling on
// somebody's behalf must not be the path that leaves their checkout payable.
//
// Post-commit and best effort. The order is already cancelled and its stock and
// credit are already back; a slow third party is not a reason to undo that. A
// failure costs nothing new — the session dies with the stock hold anyway, and
// money that beats it arrives as payment.ErrOrderCancelled, which is refused and
// recorded rather than captured.
func (h *Handler) closeSessions(ctx context.Context, number string, sessions []string) {
	if h.sessions == nil {
		return
	}
	for _, id := range sessions {
		if err := h.sessions.ExpireSession(ctx, id); err != nil {
			// Warn rather than Error: Stripe refuses to expire anything but an
			// OPEN session, so a checkout the customer completed a moment ago
			// lands here. That is the provider deciding whether money is in
			// flight, which is exactly what goen must not decide for itself.
			h.log.WarnContext(ctx, "expire checkout session of a cancelled order",
				"order_number", number, "session_id", id, "error", err)
		}
	}
}

// RequireStaff wraps a back-office handler.
//
// A signed-out visitor gets the sign-in page; a signed-in CUSTOMER gets a 404,
// not a 403. A 403 confirms that /admin is a real place with something behind
// it, which is worth more to someone probing than the accuracy is to a customer
// who has no business here.
func (h *Handler) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Signed out and signed-in-but-not-staff get the SAME answer, and it is
		// the answer an absent page gives.
		//
		// Redirecting a signed-out visitor to /signin?next=/admin would tell
		// them /admin is real — the exact disclosure the 404 for customers is
		// chosen to avoid — and would write the back-office path into their
		// history and referrer on the way. One branch saying "this does not
		// exist" while another says "sign in and it will" is not a policy.
		//
		// The cost is a staff member with an expired session seeing a 404
		// instead of a login prompt. They sign in at /signin and come back;
		// that is a smaller price than advertising where the back office is.
		u, ok := account.FromContext(r.Context())
		if !ok || !u.IsAdmin() {
			web.Render(w, r, h.log, http.StatusNotFound, pages.Notice(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNotFoundTitle)}, "404",
				i18n.T(r.Context(), i18n.KeyAdminNotFoundHead),
				i18n.T(r.Context(), i18n.KeyAdminNotFoundBody)))
			return
		}

		// The SECOND factor, checked here rather than at sign-in.
		//
		// One gate in front of every back-office route is what makes the
		// guarantee auditable: no refund, no store credit, no stock adjustment
		// without a code verified in this session and recent. Checking it in
		// the login flow instead would need a half-authenticated state to live
		// somewhere, and a session that "does not count yet" eventually counts.
		//
		// The redirect goes to /admin/verify, which IS a disclosure — but only
		// to somebody who has already proved they are staff, so it discloses
		// nothing the 404 above was protecting.
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

// RequireAdmin wraps a back-office handler that changes WHO WORKS HERE.
//
// Everything else in the back office is a staff job: orders, stock, refunds,
// the catalogue. Deciding who holds a back-office account is not, so it asks a
// role predicate of its own. Gated on the same one as everything else, `staff`
// and `admin` are indistinguishable at every point a request is decided, and the
// four /admin/staff routes are a self-service promotion desk.
//
// It wraps RequireStaff rather than repeating it, so the second factor, the
// 404-not-403 disclosure rule and the session check stay in ONE place. A second
// copy of that reasoning is a second place for it to drift.
//
// A staff member who reaches one of these gets the same 404 a customer gets at
// /admin, for the same reason: it is the answer an absent page gives, and
// telling somebody their colleagues' page exists but is not for them is worth
// more to a prober than the accuracy is to them.
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
		// number passed IsOrderNumber: GO- followed by digits and one hyphen, so
		// it is a path segment and cannot carry a scheme, a second slash or a
		// query separator. gosec's taint analysis does not follow the check.
		http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	case errors.Is(err, ErrRefused):
		// The database refused the transition and its message names the rule.
		// Logged in full; the page says the move was refused, because a
		// constraint name is not something a shop assistant can act on.
		h.log.WarnContext(r.Context(), "order transition refused",
			"order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by IsOrderNumber
	default:
		h.log.ErrorContext(r.Context(), "advance order", "error", err)
		h.serverError(w, r)
	}
}

// Ship serves POST /admin/orders/{number}/ship.
//
// A plain form: carrier, tracking number, and how many of each line are in this
// parcel. It records the shipment, settles the stock those units were holding
// and appends to the order's history, all together — see [Store.Ship] for why
// none of those may happen without the others.
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

// parcelLines reads how many of each order line the dispatch form is sending.
//
// Named `qty_<order_line_id>` rather than parallel arrays, for the reason the
// return inspection form uses the same shape: a browser is free to reorder
// repeated fields, and two lists that drifted apart would ship the wrong line's
// quantity.
//
// A nil map means "everything outstanding", which is what a form with no
// quantity fields at all sends — the whole order in one parcel, which is still
// the common case.
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

// staffID is who is acting, for the history. A staff member always has a
// session here — RequireStaff would not have let the request through otherwise
// — so an unparseable id is a bug, and the event records "nobody" rather than
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

// ReceiveStock serves POST /admin/stock/receive.
//
// Separate from AdjustStock rather than a "reason" dropdown beside it, because
// the two are different acts and a shop should not have to pick the right word
// out of a list to record the ordinary one. It redirects back to the LEDGER the
// form lives on, so the row it just wrote is the first thing on screen.
func (h *Handler) ReceiveStock(w http.ResponseWriter, r *http.Request) {
	u, _ := account.FromContext(r.Context())
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	sku := r.PostFormValue("sku")
	// Built from the SKU the FORM carried, so a refusal returns to the ledger
	// that posted it. web.SitePath is not needed: the SKU is a path segment this
	// server matched a route on, and the redirect is relative by construction.
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
		// Most often sale_campaign_variant_still_valid: retiring the last
		// discounted variant of a product a campaign features.
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
		// product_variants_compare_at_is_higher: a "sale" that is not a saving.
		h.log.WarnContext(r.Context(), "reprice refused",
			"sku", r.PostFormValue("sku"), "error", err)
		http.Redirect(w, r, "/admin/stock?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set variant price", "error", err)
		h.serverError(w, r)
	}
}

// adminNotices is the one-shot message each redirect parameter carries.
//
// A table rather than a switch, because it is one: every arm would ask the same
// question of a different key and answer with a constant, which is a lookup
// written out longhand — two dozen of them past what any complexity check
// allows. Order does not matter, because a redirect sets exactly one.
//
// i18n-exempt: the back office is the staff of one Taiwanese shop, which is the
// category exemption the chrome-language rule already names.
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

// noticeFor turns the one-shot query parameter a redirect carries into the
// message the page shows.
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

// Decide serves POST /admin/returns/{id}/decide.
//
// Approving pays money back, so a refusal from the database or from Stripe is
// reported rather than swallowed: an approval that silently failed would leave
// a customer told they were refunded and no money moved.
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
		// A Stripe failure lands here. The refund row is already committed as
		// 'failed', so the money is a job for a human rather than a lost write.
		h.log.ErrorContext(r.Context(), "decide return",
			"return", r.PathValue("id"), "error", err)
		http.Redirect(w, r, "/admin/returns?refundfailed=1", http.StatusSeeOther)
	}
}

// Inspect serves POST /admin/returns/{id}/inspect.
//
// One form for the whole parcel: a return of three things is opened once, and
// three separate submissions would leave the shop able to record two lines and
// forget the third — which is the state return_requests_completed_is_inspected
// then refuses to close, with nothing on screen saying why.
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

// inspectionLines reads the per-line counts off the form.
//
// Named `received_<order_line_id>` and `restocked_<order_line_id>` rather than
// parallel arrays, because a browser is free to reorder repeated fields and two
// lists that drifted apart would restock the wrong variant — the same reason the
// return form itself keys quantities by line id.
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
		// Absent means zero. A staff member who leaves the restock box empty on a
		// line that arrived broken is saying "none of it", and reading that as
		// "all of it" would put damaged goods back on the shelf.
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
		// return_requests_completed_is_inspected lands here: a parcel nobody has
		// opened cannot be closed, and the page says so rather than showing a
		// constraint name.
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

// creditNotice is the credit page's own version of noticeFor.
//
// It exists to say the BALANCE after a grant. The form is a blank box, so
// granting again because the first grant was not visible anywhere is how a
// customer ends up with twice what they were owed — and the number is what
// CreditBalance answers.
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

// GrantCredit serves POST /admin/credit.
//
// The amount is typed in DOLLARS and stored in cents. A staff member giving a
// customer NT$500 types 500, not 50000 — a form that asks for cents is a form
// that eventually gives somebody a hundred times too much.
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
		// The resulting balance travels as a number, never the address it
		// belongs to: a query string is logged, and whose balance it is would
		// be the personal half of that pair.
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
	// Read separately rather than joined into Product: an image list is a
	// different cardinality from a product row, and folding it in would make
	// every other caller of Product pay for a join it does not read.
	if images, imgErr := h.store.ProductImages(r.Context(), r.PathValue("slug")); imgErr != nil {
		// Not fatal. Losing the image strip is much smaller than losing the
		// page a staff member came here to edit.
		h.log.ErrorContext(r.Context(), "read product images", "error", imgErr)
	} else {
		view.Images = images
	}
	// Images already uploaded, so a shot that belongs on three products is
	// attached three times rather than uploaded three times. Content addressing
	// deduplicates the BYTES; without a picker the staff work is still repeated.
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
		// Zero is UNMEASURED and stores NULL, so a blank field leaves the
		// variant refused by no shipping method rather than blocked from all of
		// them.
		ParcelLongestMM: parseSafetyStock(r.PostFormValue("parcel_longest")),
		ParcelSumMM:     parseSafetyStock(r.PostFormValue("parcel_sum")),
		ParcelWeightG:   parseSafetyStock(r.PostFormValue("parcel_weight")),
		// One select per option, all named option_value. PostForm holds them in
		// document order, which is the order the page rendered the axes.
		OptionValues: r.PostForm["option_value"],
	}
	errs, err := h.store.AddVariant(r.Context(), slug, f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		// Re-rendered at 422 with the refusal on the page rather than redirected,
		// because "every axis needs a value" is a sentence about what was submitted
		// and a query parameter cannot say which axis was missing.
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

// dollarsToCents reads a price typed in DOLLARS.
//
// The form asks for dollars because a staff member pricing something at NT$500
// types 500. A form that asks for cents is a form that eventually prices
// something at a hundredth of what was meant.
func dollarsToCents(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 || n > MaxPriceCents/100 {
		return 0
	}
	return n * 100
}

// parseSafetyStock reads the safety-stock field.
//
// It returns int32 directly rather than converting a bounded int64 at the call
// site: gosec cannot see that parseBounded's ceiling makes the conversion safe,
// and a //nolint would be asserting the bound rather than expressing it.
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

// small reads a small count, bounded well below int32 so no conversion can
// overflow.
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
		// sale_campaign_needs_discount speaks here when the product has nothing
		// marked down, which is the refusal a staff member most needs told.
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

// UploadImage serves POST /admin/products/{slug}/images.
//
// Two writes that are deliberately NOT one transaction: the image is stored,
// then attached. Storing is idempotent by content, so a failure between the two
// leaves an orphan that UnreferencedMedia reclaims — whereas holding an 8 MB
// upload inside the attach transaction would hold a row lock for the length of
// a decode.
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	// Read the alt text BEFORE the file: ParseMultipartForm populates both, and
	// a rejected image should not lose what was typed beside it.
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
		// The reason is read from the error, never assumed. One query for every
		// failure — "?noalt=1" whatever went wrong — tells a staff member whose
		// alt text was fine to fill it in, which is a message that sends
		// somebody to fix the one thing that was not wrong.
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// ReuseImage serves POST /admin/products/{slug}/images/reuse.
//
// Attaches an image ALREADY uploaded, which is what content addressing is for:
// `product_images_storage_key_key` is (product_id, storage_key) precisely so a
// generic accessory shot can sit on two products. Without a picker the staff
// member re-uploads the same file for every product — the bytes deduplicate and
// the work does not.
func (h *Handler) ReuseImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

	// The dimensions come from the STORED object rather than the form: they are
	// what the srcset is built from, and a hand-edited pair would make a
	// browser lay out against a size the image does not have.
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
		// Zero is "no stated limit", the honest default for 宅配.
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

// SetShippingMethodActive serves POST /admin/shipping/method/{id}/active.
//
// A method is switched OFF, never deleted: shipping_method_versions references it
// ON DELETE RESTRICT, and every past order names the version it was priced from — a
// method that ever carried a parcel is part of the record.
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
		// Prefixes or surcharges still point at it, which is a sentence a staff
		// member can act on rather than a constraint name.
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

// dollars reads a whole-dollar amount, treating a blank or unparseable box as zero.
//
// Zero is a real answer for the free-over threshold (there isn't one) and a refused
// one for the fee, which NewMethod.Validate decides — the parse does not need to
// distinguish them.
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

// EditFAQEntry serves POST /admin/faq/{id}.
//
// Save and delete on one endpoint, chosen by the submitted button, for the reason
// the taxonomy rows do it: one decision a staff member makes on one row.
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

// optionWrite is the shape both option forms share: parse, write, and either
// re-render with the refusal or answer 303.
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
		// Refused for what was typed. The page re-renders WITH the values, the
		// same as every other rejected form here.
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

// uploadReason turns a rejection into the query the page reads.
//
// The two cases a staff member can act on are named; everything else is the
// generic one. Telling somebody WHICH decoder refused their file would be
// telling an attacker which decoders are wired up.
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
	// The strip belongs on this page for the same reason the hero does: both are
	// what the storefront says about itself before a visitor has chosen anything.
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

// CreateHeroSlide serves POST /admin/home.
//
// Multipart, because the artwork arrives with the copy. The image is optional:
// a slide with none falls back to the built-in artwork, which is better than
// refusing a text change because nobody had a photograph ready.
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

// CreateTaxon serves POST /admin/taxonomy/{kind}.
//
// One handler for both, because they differ in one call. Two would be two
// places to forget the audit event, and the kind is a path value the router
// constrains rather than anything a form supplies.
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

// EditTaxon serves POST /admin/taxonomy/{kind}/{slug}.
//
// Rename and delete on one endpoint, chosen by the submitted button. Two
// separate routes would be two RequireStaff wrappings and two audit call sites
// for what is one decision a staff member makes on one row.
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
	// An unparseable or out-of-range window falls back to the default rather
	// than erroring: the number reaches a query that scans order history, and
	// an allowlist in the store is what bounds the work — a 400 here would just
	// be a worse way to say the same thing.
	// A parse failure is zero, which the store's allowlist turns into the
	// default — the same answer an out-of-range number gets, because both mean
	// "not one of the windows this page offers".
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

// AnswerQuestion serves POST /admin/questions/{id}.
//
// Answer and hide on one endpoint, chosen by the submitted button: they are one
// decision a staff member makes about one question.
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
		// staff=true, because this endpoint IS the shop. The flag is stored
		// with the answer rather than re-derived later — see
		// product_answers.is_staff.
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
	// An empty threshold is "no free shipping", not zero — and ParseInt refuses
	// "" rather than answering 0, which is why it is read separately.
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
		// Optional, and the checkout's chooser is what reads them.
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
	// An empty box means zero here, which CLEARS the surcharge: the field is
	// rendered blank when there is none, so submitting it untouched must be a
	// no-op rather than a parse error.
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

// NewsletterIssueLimit bounds the issue list. A newsletter is monthly; fifty is
// four years of them, and nobody scrolls further than that in a back office.
const NewsletterIssueLimit = 50

// Newsletter serves GET /admin/newsletter.
//
// This is the reading end of a list the footer writes to: without it the shop
// collects addresses and no page can see them, which is a subscriber list that
// exists and cannot be used.
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

// SendNewsletter serves POST /admin/newsletter/{id}/send.
//
// The one irreversible button in the back office. It answers 303 so a reload
// cannot resubmit it, and the store refuses a second send in the UPDATE's own
// WHERE clause — because a reload is not the only way two of these arrive.
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

// Warranties serves GET /admin/warranty.
//
// The shop's half of warranty registration. The customer registers a unit and
// reads their own cover; without this nobody at the shop can see either — while
// /warranty promises the shop collects the unit and pays the carriage. A claim
// arrives and the only record of it is in the hands of the person making it.
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

// IssueInvoice serves POST /admin/orders/{number}/invoice.
//
// The 發票 preference is collected at checkout and this is what acts on it.
// Without it a staff member packing an order can see that it needs a 統編
// invoice and has no way to issue one.
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
		// The 加值中心's own reason. Logged in full because it names the field
		// to fix, and shown as a single message because a staff member cannot
		// act on an RtnCode.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the invoice",
			"order", number, "error", err)
		//nolint:gosec // G710: validated by IsOrderNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicefailed=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "issue invoice", "order", number, "error", err)
		h.serverError(w, r)
	}
}

// VoidInvoice serves POST /admin/orders/{number}/invoice/void.
//
// A 統一發票 cannot be edited. A wrong one is voided and a correct one issued in
// its place, which is what invoice_documents_guard enforces from the database's
// side and what the form's wording tells a staff member.
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
