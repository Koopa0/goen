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
	switch {
	case err == nil:
		payment.CloseSessions(r.Context(), h.sessions, h.log, number, sessions)
		http.Redirect(w, r, back+"?refunded=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, ErrUnsettled):
		h.log.WarnContext(r.Context(), "refund before shipment has not settled", "order", number, "error", err)
		http.Redirect(w, r, back+"?refundpending=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case pgerr.IsConstraint(err, "orders_cancel_invoice_resolved"):
		h.log.WarnContext(r.Context(), "refund before shipment waits on the invoice", "order", number, "error", err)
		http.Redirect(w, r, back+"?cancelinvoice=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, refundstate.ErrIncomplete):
		// Tested before ErrRefused: a payout may carry a database refusal as its
		// cause, but the refund is open and Resume is what the staff member needs.
		h.log.ErrorContext(r.Context(), "refund before shipment", "order", number, "error", err)
		http.Redirect(w, r, back+"?refundretry=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	case errors.Is(err, refundstate.ErrRefused), errors.Is(err, ErrInvalid):
		h.log.WarnContext(r.Context(), "refund before shipment refused", "order", number, "error", err)
		http.Redirect(w, r, back+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
	default:
		h.log.ErrorContext(r.Context(), "refund before shipment", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) confirmRefundBeforeShipment(w http.ResponseWriter, r *http.Request, number string) bool {
	view, err := h.store.RefundPreview(r.Context(), number)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
		return true
	case errors.Is(err, refundstate.ErrRefused):
		h.log.WarnContext(r.Context(), "refund before shipment not offered", "order", number, "error", err)
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther) //nolint:gosec // G710: validated by order.ValidNumber
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
