package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/ordernumber"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
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
	sessions payment.SessionCloser
	store    *Store
	log      *slog.Logger
}

type HandlerDeps struct {
	Store    *Store
	Images   *media.Handler
	Outbox   *outbox.Store
	Letters  *newsletter.Store
	Log      *slog.Logger
	Sessions payment.SessionCloser
}

func NewHandler(d HandlerDeps) *Handler {
	if d.Store == nil || d.Images == nil || d.Outbox == nil || d.Letters == nil || d.Log == nil {
		panic("admin: NewHandler requires a store, a media handler, an outbox, " +
			"a newsletter store and a logger")
	}
	return &Handler{
		store: d.Store, images: d.Images, outbox: d.Outbox, letters: d.Letters,
		log: d.Log, sessions: d.Sessions,
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
	sessions, err := h.store.Advance(r.Context(), number, ParseStatus(r.PostFormValue("status")), audit.ActorID(r.Context()))
	switch {
	case err == nil:
		payment.CloseSessions(r.Context(), h.sessions, h.log, number, sessions)
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
	}, audit.ActorID(r.Context()))
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

var adminNotices = map[string]i18n.Key{
	"ok":             i18n.KeyAdminNoticeOK,
	"refused":        i18n.KeyAdminNoticeRefused,
	"shipped":        i18n.KeyAdminNoticeShipped,
	"toolate":        i18n.KeyAdminNoticeTooLate,
	"deliveryneeds":  i18n.KeyAdminNoticeDeliveryNeeds,
	"imageneeds":     i18n.KeyAdminNoticeImageNeeds,
	"toobig":         i18n.KeyAdminNoticeTooBig,
	"notimage":       i18n.KeyAdminNoticeNotImage,
	"losslesswebp":   i18n.KeyAdminNoticeLosslessWebP,
	"uploadfailed":   i18n.KeyAdminNoticeUploadFailed,
	"uploadbusy":     i18n.KeyAdminNoticeUploadBusy,
	"attachrefused":  i18n.KeyAdminNoticeAttachRefused,
	"noalt":          i18n.KeyAdminNoticeNoAlt,
	"badoption":      i18n.KeyAdminNoticeBadOption,
	"paidcancel":     i18n.KeyAdminNoticePaidCancel,
	"refunded":       i18n.KeyAdminNoticeRefunded,
	"refundpending":  i18n.KeyAdminNoticeRefundPending,
	"cancelinvoice":  i18n.KeyAdminNoticeCancelInvoice,
	"refundretry":    i18n.KeyAdminNoticeRefundRetry,
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
	"specfailed":     i18n.KeyAdminNoticeSpecFailed,
}

func noticeFor(r *http.Request) string {
	return web.Notice(r, adminNotices)
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
