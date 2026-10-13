package orders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where no session was ever opened.
	sessions payment.SessionCloser
	store    *Store
	log      *slog.Logger
}

func NewHandler(store *Store, sessions payment.SessionCloser, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("orders: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, sessions: sessions, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin", ac.RequireStaff(h.Dashboard))
	mux.HandleFunc("GET /admin/orders", ac.RequireStaff(h.List))
	mux.HandleFunc("GET /admin/orders/picking/slips", ac.RequireStaff(h.PickingSlips))
	mux.HandleFunc("GET /admin/orders/{number}", ac.RequireStaff(h.Order))
	mux.HandleFunc("POST /admin/orders/{number}/status", ac.RequireStaff(h.Advance))
	mux.HandleFunc("POST /admin/orders/{number}/ship", ac.RequireStaff(h.Ship))
	mux.HandleFunc("POST /admin/orders/{number}/note", ac.RequireStaff(h.StaffNote))
	mux.HandleFunc("POST /admin/orders/{number}/delivery", ac.RequireStaff(h.CorrectDelivery))
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read dashboard", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if health, err := h.store.HealthTasks(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "read health tasks for the dashboard", "error", err)
		view.HealthUnavailable = true
	} else {
		view.Tasks = append(view.Tasks, health...)
	}
	if err := h.store.FillWeek(r.Context(), &view, time.Now()); err != nil {
		h.log.ErrorContext(r.Context(), "read the last seven days for the dashboard", "error", err)
		view.WeekUnavailable = !errors.Is(err, admin.ErrLatestPaid)
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Dashboard(admin.Meta(r.Context()), view))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.List(r.Context(),
		ParseQueueFilter(r.URL.Query().Get("status")), r.URL.Query().Get("q"), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read orders", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Orders(admin.OrdersMeta(r.Context()), view))
}

func (h *Handler) Order(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, admin.MissingRecord(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminNoOrderTitle)},
				admin.MissingRecordView{Section: "orders", Heading: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminNoOrderHead), r.PathValue("number")), Body: i18n.T(r.Context(), i18n.KeyAdminNoOrderBody), BackLabel: i18n.KeyAdminBackOrders, OrderNumber: r.PathValue("number")}))
			return
		}
		h.log.ErrorContext(r.Context(), "read order", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	logUnrecognized(r.Context(), h.log, view.Number, view.Timeline)
	view.Notice = web.Notice(r, notices)
	view.AllowanceOperationID = uuid.NewString()
	web.Render(w, r, h.log, http.StatusOK,
		admin.Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

func (h *Handler) Advance(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		payment.CloseSessions(r.Context(), h.sessions, h.log, number, sessions)
		http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, ErrPaidCancel), pgerr.IsConstraint(err, "orders_paid_cancel_needs_refund"):
		http.Redirect(w, r, "/admin/orders/"+number+"?paidcancel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case pgerr.IsConstraint(err, "orders_funded_to_leave_pending"):
		http.Redirect(w, r, "/admin/orders/"+number+"?unfunded=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case pgerr.IsConstraint(err, "orders_finished_when_shipped"):
		http.Redirect(w, r, "/admin/orders/"+number+"?owesparcel=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, ErrRefused):
		// Logged in full; the page names the rule only for the refusals a shop
		// assistant can act on, because a constraint name is not one of them.
		h.log.WarnContext(r.Context(), "order transition refused",
			"order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
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
		h.rejectShip(w, r, &shipRefusal{notice: i18n.KeyAdminDispatchRefused})
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
// redirect would put on the warehouse. An order that no longer takes a dispatch
// has no form to refill, so its notice names the carrier and number instead:
// the parcel may already be out of the door.
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
	if refusal.notice != "" {
		text := say(refusal.notice)
		if refusal.notice == i18n.KeyAdminDispatchRefused {
			text = fmt.Sprintf(text, i18n.CarrierName(r.Context(), carrier.Carrier(view.ShipCarrier)), view.ShipTracking)
		}
		view.Notice = components.Result{Outcome: components.OutcomeRefused, Text: text}
	}
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
		access.ServerError(w, r, h.log)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+number+"?ok=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
}

var notices = map[string]web.NoticeEntry{
	"ok":              web.Done(i18n.KeyAdminNoticeOK),
	"refused":         web.Refused(i18n.KeyAdminNoticeRefused),
	"shipped":         web.Done(i18n.KeyAdminNoticeShipped),
	"toolate":         web.Refused(i18n.KeyAdminNoticeTooLate),
	"deliveryneeds":   web.Refused(i18n.KeyAdminNoticeDeliveryNeeds),
	"paidcancel":      web.Refused(i18n.KeyAdminNoticePaidCancel),
	"refunded":        web.Done(i18n.KeyAdminNoticeRefunded),
	"refundpending":   web.Failed(i18n.KeyAdminNoticeRefundPending),
	"cancelinvoice":   web.Failed(i18n.KeyAdminNoticeCancelInvoice),
	"refundretry":     web.Failed(i18n.KeyAdminNoticeRefundRetry),
	"cancelretry":     web.Failed(i18n.KeyAdminNoticeCancelRetry),
	"refundshipped":   web.Refused(i18n.KeyAdminNoticeRefundShipped),
	"refundhasreturn": web.Refused(i18n.KeyAdminNoticeRefundHasReturn),
	"refundcancelled": web.Refused(i18n.KeyAdminNoticeRefundCancelled),
	"refundunpaid":    web.Refused(i18n.KeyAdminNoticeRefundUnpaid),
	"refundchanged":   web.Refused(i18n.KeyAdminNoticeRefundChanged),
	"refundpicking":   web.Refused(i18n.KeyAdminNoticeRefundPicking),
	"refundreason":    web.Refused(i18n.KeyAdminRefundErrReason),
	"refundmismatch":  web.Failed(i18n.KeyAdminNoticeRefundMismatch),
	"refundunsure":    web.Failed(i18n.KeyAdminNoticeRefundUnsure),
	"unfunded":        web.Refused(i18n.KeyAdminNoticeUnfunded),
	"owesparcel":      web.Refused(i18n.KeyAdminNoticeOwesParcel),
	"invoiced":        web.Done(i18n.KeyAdminNoticeInvoiced),
	"voided":          web.Done(i18n.KeyAdminNoticeVoided),
	"hasinvoice":      web.Refused(i18n.KeyAdminNoticeHasInvoice),
	"noinvoice":       web.Refused(i18n.KeyAdminNoticeNoInvoice),
	"invoicefailed":   web.Failed(i18n.KeyAdminNoticeInvoiceFailed),
	"invoicingoff":    web.Refused(i18n.KeyAdminNoticeInvoicingOff),
	"invoicepending":  web.Failed(i18n.KeyAdminNoticeInvoicePending),
	"allowed":         web.Done(i18n.KeyAdminNoticeAllowed),
	"allowsent":       web.Done(i18n.KeyAdminNoticeAllowSent),
	"allowtoomuch":    web.Refused(i18n.KeyAdminNoticeAllowTooMuch),
	"allowclaimed":    web.Refused(i18n.KeyAdminNoticeAllowClaimed),
	"voidreason":      web.Refused(i18n.KeyAdminNoticeVoidReason),
	"voidfailed":      web.Failed(i18n.KeyAdminNoticeVoidFailed),
	"allowfailed":     web.Failed(i18n.KeyAdminNoticeAllowFailed),
}

func (h *Handler) CorrectDelivery(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		h.rejectDelivery(w, r, submitted, []web.FieldRefusal{{Field: "postal_code", MessageKey: refused.Key}})
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
func (h *Handler) rejectDelivery(w http.ResponseWriter, r *http.Request, d *DeliveryCorrection, refusals []web.FieldRefusal) {
	view, err := h.store.Order(r.Context(), r.PathValue("number"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused delivery correction", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	applyDeliveryRefusals(r.Context(), &view, d, refusals)
	view.AllowanceOperationID = uuid.NewString()
	web.Render(w, r, h.log, http.StatusUnprocessableEntity,
		admin.Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), view.Number)}, &view))
}

func applyDeliveryRefusals(ctx context.Context, view *admin.OrderView, d *DeliveryCorrection, refusals []web.FieldRefusal) {
	view.Delivery = admin.Delivery(*d)
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
			view.Notice = components.Result{Outcome: components.OutcomeRefused, Text: view.DeliveryErrors[field]}
		}
	}
}
