package refunds

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/refundstate"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	// sessions closes a cancelled order's checkout at the payment provider. Nil
	// on a deployment with no Stripe key, where no session was ever opened.
	sessions payment.SessionCloser
	log      *slog.Logger
}

func NewHandler(store *Store, sessions payment.SessionCloser, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("refunds: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, sessions: sessions, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("POST /admin/orders/{number}/refund", ac.RequireStaff(h.RefundBeforeShipment))
}

// RefundBeforeShipment serves POST /admin/orders/{number}/refund. The first
// POST only renders what the refund pays; the confirmed one moves money.
func (h *Handler) RefundBeforeShipment(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	if h.confirmRefundBeforeShipment(w, r, number) {
		return
	}
	back := "/admin/orders/" + number
	sessions, err := h.store.RefundBeforeShipment(r.Context(), number, r.PostFormValue("reason"))
	outcome, ok := refundNotice(err)
	if !ok {
		h.log.ErrorContext(r.Context(), "refund before shipment", "order", number, "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	if err == nil {
		payment.CloseSessions(r.Context(), h.sessions, h.log, number, sessions)
	} else {
		h.log.Log(r.Context(), outcome.Level, "refund before shipment", "order", number, "error", err)
	}
	http.Redirect(w, r, back+outcome.Query, http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
}

// refundOutcome is where a refund press sends the staff member and how loudly
// the log records it: Error for what needs a person, Warn for a refusal.
type refundOutcome struct {
	Query string
	Level slog.Level
}

// refundNotice is the outcome that selects the order page notice for what
// RefundBeforeShipment or RefundPreview returned, and false for an error no
// notice describes. Recovery comes before the refusals: a payout or
// cancellation can retain a database refusal as its cause while the approved
// refund remains open, and the refusal's sentence would then claim nothing moved.
func refundNotice(err error) (refundOutcome, bool) {
	switch {
	case err == nil:
		return refundOutcome{Query: "?refunded=1", Level: slog.LevelInfo}, true
	case errors.Is(err, ErrUnsettled):
		return refundOutcome{Query: "?refundpending=1", Level: slog.LevelWarn}, true
	case pgerr.IsConstraint(err, "orders_cancel_invoice_resolved"):
		return refundOutcome{Query: "?cancelinvoice=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrCancellationIncomplete):
		return refundOutcome{Query: "?cancelretry=1", Level: slog.LevelError}, true
	case errors.Is(err, refundstate.ErrIncomplete):
		return refundOutcome{Query: "?refundretry=1", Level: slog.LevelError}, true
	case errors.Is(err, ErrShipped):
		return refundOutcome{Query: "?refundshipped=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrHasReturn):
		return refundOutcome{Query: "?refundhasreturn=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrOrderCancelled), pgerr.IsConstraint(err, "orders_history_frozen"):
		return refundOutcome{Query: "?refundcancelled=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrNotPaid):
		return refundOutcome{Query: "?refundunpaid=1", Level: slog.LevelWarn}, true
	case pgerr.IsConstraint(err, "orders_paid_cancel_needs_refund"):
		return refundOutcome{Query: "?refundpicking=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrOrderChanged),
		pgerr.IsConstraint(err, "return_before_shipment_eligible"),
		pgerr.IsConstraint(err, "orders_legal_transition"):
		return refundOutcome{Query: "?refundchanged=1", Level: slog.LevelWarn}, true
	case errors.Is(err, ErrPayoutUnfit),
		pgerr.IsConstraint(err, "refunds_sources_cover_return"),
		pgerr.IsConstraint(err, "refunds_credit_attribution"),
		pgerr.IsConstraint(err, "refunds_card_amount_positive"),
		pgerr.IsConstraint(err, "refunds_return_captured"):
		return refundOutcome{Query: "?refundmismatch=1", Level: slog.LevelError}, true
	case errors.Is(err, ErrInvalid):
		return refundOutcome{Query: "?refundreason=1", Level: slog.LevelWarn}, true
	case errors.Is(err, refundstate.ErrRefused):
		return refundOutcome{Query: "?refundunsure=1", Level: slog.LevelError}, true
	}
	return refundOutcome{}, false
}

func (h *Handler) confirmRefundBeforeShipment(w http.ResponseWriter, r *http.Request, number string) bool {
	view, err := h.store.RefundPreview(r.Context(), number)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
		return true
	case errors.Is(err, refundstate.ErrRefused):
		outcome, _ := refundNotice(err)
		h.log.WarnContext(r.Context(), "refund before shipment not offered", "order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+outcome.Query, http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
		return true
	case err != nil:
		h.log.ErrorContext(r.Context(), "read refund before shipment", "order", number, "error", err)
		access.ServerError(w, r, h.log)
		return true
	}
	view.Reason = r.PostFormValue("reason")
	status := http.StatusOK
	switch {
	case r.PostFormValue("confirm") != "refund" ||
		r.PostFormValue("total") != strconv.FormatInt(view.TotalCents, 10):
	case !view.Resume && (strings.TrimSpace(view.Reason) == "" || !returns.ValidResolution(view.Reason)):
		// The reason becomes the provider refund's and the audit's; a resume
		// reuses the one already recorded.
		view.ReasonInvalid = true
		status = http.StatusUnprocessableEntity
	default:
		return false
	}
	web.Render(w, r, h.log, status, admin.ConfirmRefund(layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminRefundTitle)}, view))
	return true
}
