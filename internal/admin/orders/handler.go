package orders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	// payments closes a cancelled order's checkout at the payment provider. Nil
	// or disabled on a deployment with no Stripe key, where no session was ever opened.
	payments *payment.Gateway
	store    *Store
	log      *slog.Logger
}

func NewHandler(store *Store, payments *payment.Gateway, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("orders: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, payments: payments, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/orders", ac.RequireStaff(h.List))
	mux.HandleFunc("GET /admin/orders/picking/slips", ac.RequireStaff(h.PickingSlips))
	mux.HandleFunc("GET /admin/orders/{number}", ac.RequireStaff(h.Order))
	mux.HandleFunc("POST /admin/orders/{number}/status", ac.RequireStaff(h.Advance))
	mux.HandleFunc("POST /admin/orders/{number}/ship", ac.RequireStaff(h.Ship))
	mux.HandleFunc("POST /admin/orders/{number}/shipments/{shipment}/delivered", ac.RequireStaff(h.DeliverShipment))
	mux.HandleFunc("POST /admin/orders/{number}/note", ac.RequireStaff(h.StaffNote))
	mux.HandleFunc("POST /admin/orders/{number}/delivery", ac.RequireStaff(h.CorrectDelivery))
	mux.HandleFunc("POST /admin/orders/{number}/invoice", ac.RequireStaff(h.IssueInvoice))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/void", ac.RequireStaff(h.VoidInvoice))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/allowance", ac.RequireStaff(h.FileAllowance))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	after := query.Get(keyset.Param)
	view, err := h.store.List(r.Context(), ParseQueueFilter(query.Get("status")), query.Get("q"), after)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read orders", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	// A complete order number that finds that order alone goes to it, unless the
	// reader is paging through a search.
	if number, ok := order.NormalizeNumber(view.Term); ok && after == "" && view.Searched &&
		len(view.Orders) == 1 && view.Orders[0].Number == number {
		http.Redirect(w, r, "/admin/orders/"+number, http.StatusSeeOther)
		return
	}
	view.PageGone = after != "" && !view.Resumed()
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Orders(Meta(r.Context()), view))
}

func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respond.Component(w, r, h.log, http.StatusNotFound, orderNotFound(
				frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNoOrderTitle)},
				frame.MissingRecordView{Section: "orders", Heading: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminNoOrderHead), r.PathValue("number")), Body: i18n.T(r.Context(), i18n.KeyAdminNoOrderBody), BackLabel: i18n.KeyAdminBackOrders}, r.PathValue("number")))
			return
		}
		h.log.ErrorContext(r.Context(), "read order", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	logUnrecognized(r.Context(), h.log, view.Number, view.Timeline)
	view.Notice = widget.NoticeFrom(r, notices)
	view.AllowanceOperationID = uuid.NewString()
	respond.Component(w, r, h.log, http.StatusOK,
		Order(frame.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

func (h *Handler) Advance(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	sessions, err := h.store.Advance(r.Context(), number, order.FulfillmentStatus(r.PostFormValue("status")), audit.ActorID(r.Context()))
	switch {
	case err == nil:
		payment.CloseSessions(r.Context(), h.payments, h.log, number, sessions)
		http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, ErrPaidCancel), db.IsConstraint(err, "orders_paid_cancel_needs_refund"):
		http.Redirect(w, r, "/admin/orders/"+number+"?paidcancel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case db.IsConstraint(err, "orders_funded_to_leave_pending"):
		http.Redirect(w, r, "/admin/orders/"+number+"?unfunded=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case db.IsConstraint(err, "orders_finished_when_shipped"):
		http.Redirect(w, r, "/admin/orders/"+number+"?owesparcel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, ErrRefused):
		// Logged in full; the page names the rule only for the refusals a shop
		// assistant can act on, because a constraint name is not one of them.
		h.log.WarnContext(r.Context(), "order transition refused",
			"order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	default:
		h.log.ErrorContext(r.Context(), "advance order", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) Ship(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
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
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?shipped=1", http.StatusSeeOther)
	case db.IsConstraint(err, "order_shipments_tracking_key"):
		h.rejectShip(w, r, &shipRefusal{tracking: i18n.KeyAdminTrackingTaken})
	case errors.Is(err, ErrQuantity):
		h.rejectShip(w, r, &shipRefusal{quantity: i18n.KeyAdminNoticeBadParcel})
	case errors.Is(err, ErrCarrier):
		h.rejectShip(w, r, &shipRefusal{carrier: i18n.KeyAdminCarrierNotForOrder})
	case errors.Is(err, ErrInvalid):
		refusal := shipRefusal{}
		if !shipping.Carrier(strings.TrimSpace(r.PostFormValue("carrier"))).Known() {
			refusal.carrier = i18n.KeyAdminNoticeNeeds
		}
		if strings.TrimSpace(r.PostFormValue("tracking")) == "" {
			refusal.tracking = i18n.KeyAdminNoticeNeeds
		}
		h.rejectShip(w, r, &refusal)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipment refused", "order", number, "error", err)
		h.rejectShip(w, r, &shipRefusal{notice: i18n.KeyAdminDispatchRefused})
	default:
		h.log.ErrorContext(r.Context(), "ship order", "error", err)
		admin.ServerError(w, r, h.log)
	}
}

type shipRefusal struct {
	carrier, tracking, quantity, notice i18n.Key
}

// rejectShip re-renders the order with everything staff typed, the carrier, the
// tracking number and each line's quantity, and the refused control marked,
// because re-typing a long tracking number after every mistake is the cost a
// redirect would put on the warehouse. An order that no longer takes a dispatch
// has no form to refill, so its notice names the carrier and number instead:
// the parcel may already be out of the door.
func (h *Handler) rejectShip(w http.ResponseWriter, r *http.Request, refusal *shipRefusal) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read order after refused dispatch", "error", err)
		admin.ServerError(w, r, h.log)
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
	if refusal.notice != "" {
		text := say(refusal.notice)
		if refusal.notice == i18n.KeyAdminDispatchRefused {
			text = fmt.Sprintf(text, shipping.Carrier(view.ShipCarrier).Name(r.Context()), view.ShipTracking)
		}
		view.Notice = widget.Result{Outcome: widget.OutcomeRefused, Text: text}
	}
	view.ShipQty = map[string]string{}
	for i := range view.Shippable {
		id := view.Shippable[i].OrderLineID
		if typed, ok := r.PostForm["qty_"+id]; ok && len(typed) > 0 {
			view.ShipQty[id] = typed[0]
		}
	}
	view.AllowanceOperationID = uuid.NewString()
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity,
		Order(frame.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
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
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	if err := h.store.SetStaffNote(r.Context(), number, r.PostFormValue("note")); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "set staff note", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
}

var notices = map[string]widget.NoticeEntry{
	"ok":              widget.NoticeDone(i18n.KeyAdminNoticeOK),
	"refused":         widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"shipped":         widget.NoticeDone(i18n.KeyAdminNoticeShipped),
	"parceldelivered": widget.NoticeDone(i18n.KeyAdminNoticeShipmentDelivered),
	"toolate":         widget.NoticeRefused(i18n.KeyAdminNoticeTooLate),
	"deliveryneeds":   widget.NoticeRefused(i18n.KeyAdminNoticeDeliveryNeeds),
	"paidcancel":      widget.NoticeRefused(i18n.KeyAdminNoticePaidCancel),
	"refunded":        widget.NoticeDone(i18n.KeyAdminNoticeRefunded),
	"refundpending":   widget.NoticeFailed(i18n.KeyAdminNoticeRefundPending),
	"cancelinvoice":   widget.NoticeFailed(i18n.KeyAdminNoticeCancelInvoice),
	"refundretry":     widget.NoticeFailed(i18n.KeyAdminNoticeRefundRetry),
	"cancelretry":     widget.NoticeFailed(i18n.KeyAdminNoticeCancelRetry),
	"refundshipped":   widget.NoticeRefused(i18n.KeyAdminNoticeRefundShipped),
	"refundhasreturn": widget.NoticeRefused(i18n.KeyAdminNoticeRefundHasReturn),
	"refundcancelled": widget.NoticeRefused(i18n.KeyAdminNoticeRefundCancelled),
	"refundunpaid":    widget.NoticeRefused(i18n.KeyAdminNoticeRefundUnpaid),
	"refundchanged":   widget.NoticeRefused(i18n.KeyAdminNoticeRefundChanged),
	"refundpicking":   widget.NoticeRefused(i18n.KeyAdminNoticeRefundPicking),
	"refundreason":    widget.NoticeRefused(i18n.KeyAdminRefundErrReason),
	"refundmismatch":  widget.NoticeFailed(i18n.KeyAdminNoticeRefundMismatch),
	"refundunsure":    widget.NoticeFailed(i18n.KeyAdminNoticeRefundUnsure),
	"unfunded":        widget.NoticeRefused(i18n.KeyAdminNoticeUnfunded),
	"owesparcel":      widget.NoticeRefused(i18n.KeyAdminNoticeOwesParcel),
	"invoiced":        widget.NoticeDone(i18n.KeyAdminNoticeInvoiced),
	"voided":          widget.NoticeDone(i18n.KeyAdminNoticeVoided),
	"hasinvoice":      widget.NoticeRefused(i18n.KeyAdminNoticeHasInvoice),
	"noinvoice":       widget.NoticeRefused(i18n.KeyAdminNoticeNoInvoice),
	"invoicefailed":   widget.NoticeFailed(i18n.KeyAdminNoticeInvoiceFailed),
	"invoicingoff":    widget.NoticeRefused(i18n.KeyAdminNoticeInvoicingOff),
	"invoicepending":  widget.NoticeFailed(i18n.KeyAdminNoticeInvoicePending),
	"allowed":         widget.NoticeDone(i18n.KeyAdminNoticeAllowed),
	"allowsent":       widget.NoticeDone(i18n.KeyAdminNoticeAllowSent),
	"allowtoomuch":    widget.NoticeRefused(i18n.KeyAdminNoticeAllowTooMuch),
	"allowclaimed":    widget.NoticeRefused(i18n.KeyAdminNoticeAllowClaimed),
	"voidreason":      widget.NoticeRefused(i18n.KeyAdminNoticeVoidReason),
	"voidfailed":      widget.NoticeFailed(i18n.KeyAdminNoticeVoidFailed),
	"allowfailed":     widget.NoticeFailed(i18n.KeyAdminNoticeAllowFailed),

	"unsentrefunded":   widget.NoticeDone(i18n.KeyAdminNoticeUnsentRefunded),
	"unsentallowance":  widget.NoticeFailed(i18n.KeyAdminNoticeUnsentAllowance),
	"unsentdiffers":    widget.NoticeRefused(i18n.KeyAdminNoticeUnsentDiffers),
	"unsentbounds":     widget.NoticeRefused(i18n.KeyAdminNoticeUnsentBounds),
	"unsentreason":     widget.NoticeRefused(i18n.KeyAdminNoticeUnsentReason),
	"unsentineligible": widget.NoticeRefused(i18n.KeyAdminNoticeUnsentIneligible),
	"unsentundecided":  widget.NoticeRefused(i18n.KeyAdminNoticeUnsentUndecided),
	"unsenthold":       widget.NoticeFailed(i18n.KeyAdminNoticeUnsentHold),
}

func (h *Handler) CorrectDelivery(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	submitted := deliveryFormOf(r.PostFormValue)
	err := h.store.CorrectDelivery(r.Context(), number, submitted)
	if refused, ok := errors.AsType[*DeliveryValidationError](err); ok {
		h.rejectDelivery(w, r, submitted, refused.Fields)
		return
	}
	if refused, ok := errors.AsType[*DeliveryPostalError](err); ok {
		h.rejectDelivery(w, r, submitted, []formtext.FieldRefusal{{Field: "postal_code", MessageKey: refused.Key}})
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
		admin.ServerError(w, r, h.log)
	}
}

// rejectDelivery keeps proposed data in the form while the summary continues
// to show the saved destination, so a refusal cannot look like a completed edit.
func (h *Handler) rejectDelivery(w http.ResponseWriter, r *http.Request, d *DeliveryCorrection, refusals []formtext.FieldRefusal) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused delivery correction", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	applyDeliveryRefusals(r.Context(), &view, d, refusals)
	view.AllowanceOperationID = uuid.NewString()
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity,
		Order(frame.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

func applyDeliveryRefusals(ctx context.Context, view *OrderView, d *DeliveryCorrection, refusals []formtext.FieldRefusal) {
	view.Delivery = Delivery(*d)
	view.DeliveryErrors = make(map[string]string, len(refusals))
	for _, refusal := range refusals {
		field := refusal.Field
		if field == "name" {
			field = "recipient"
		}
		if _, seen := view.DeliveryErrors[field]; !seen {
			view.DeliveryErrors[field] = i18n.T(ctx, refusal.MessageKey)
		}
		var shown bool
		switch field {
		case "recipient", "phone", "email":
			shown = true
		case "postal_code", "city", "district", "street":
			shown = !view.PickupDestination
		case "pickup_chain", "pickup_store_code", "pickup_store_name":
			shown = view.PickupDestination
		}
		if !shown && view.Notice.Text == "" {
			view.Notice = widget.Result{Outcome: widget.OutcomeRefused, Text: view.DeliveryErrors[field]}
		}
	}
}
