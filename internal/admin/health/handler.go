package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type NamedPool struct {
	Name string
	Pool *pgxpool.Pool
}

type Handler struct {
	store    *Store
	messages *outbox.Store
	pools    []NamedPool
	disputes DisputeSource
	log      *slog.Logger
}

// NewHandler returns the health page's handler. disputes may be nil, which
// leaves the disputes row off the page.
func NewHandler(
	store *Store, messages *outbox.Store, pools []NamedPool, disputes DisputeSource, log *slog.Logger,
) *Handler {
	if store == nil || messages == nil || log == nil {
		panic("health: NewHandler requires a store, an outbox and a logger")
	}
	return &Handler{store: store, messages: messages, pools: pools, disputes: disputes, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/health", ac.RequireStaff(h.Page))
	mux.HandleFunc("POST /admin/health/reconcile", ac.RequireStaff(h.Reconcile))
}

var notices = map[string]web.NoticeEntry{
	"reconciled":    web.Done(i18n.KeyAdminNoticeReconciled),
	"invoicequeued": web.Done(i18n.KeyAdminNoticeInvoiceQueued),
	"notflagged":    web.Done(i18n.KeyAdminNoticeNotFlagged),
	"mustrefund":    web.Refused(i18n.KeyAdminNoticePaymentMustRefund),
}

// Reconcile records an explicit money outcome: an event is released only after full
// refund/already-succeeded accounting, while a provider-complete payment chooses
// paid attribution or confirmed-unpaid/refunded. It also grants one Allowance
// resend after a human confirms provider absence. The three subjects and their
// outcomes use distinct form fields and database doors.
func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	invoiceQueued, err := h.applyHealthReconciliation(
		r.Context(), healthReconcileSubmissionOf(r),
	)
	switch {
	case err == nil:
		if invoiceQueued {
			http.Redirect(w, r, "/admin/health?invoicequeued=1", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin/health?reconciled=1", http.StatusSeeOther)
	case errors.Is(err, ErrPaymentRequiresRefund):
		http.Redirect(w, r, "/admin/health?mustrefund=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/health?notflagged=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "reconcile payment", "error", err)
		access.ServerError(w, r, h.log)
	}
}

type healthReconcileSubmission struct {
	eventID              string
	providerRef          string
	invoiceOperation     string
	eventResolutionOK    bool
	completeResolution   CompletePaymentResolution
	completeResolutionOK bool
	invoiceResolutionOK  bool
}

func healthReconcileSubmissionOf(r *http.Request) healthReconcileSubmission {
	completeResolution, completeResolutionOK := parseCompletePaymentResolution(
		r.PostFormValue("resolution"),
	)
	return healthReconcileSubmission{
		eventID:              strings.TrimSpace(r.PostFormValue("event")),
		providerRef:          strings.TrimSpace(r.PostFormValue("payment")),
		invoiceOperation:     strings.TrimSpace(r.PostFormValue("invoice_operation")),
		eventResolutionOK:    paymentEventSafeReleaseSubmitted(r.PostFormValue("event_resolution")),
		completeResolution:   completeResolution,
		completeResolutionOK: completeResolutionOK,
		invoiceResolutionOK:  r.PostFormValue("invoice_resolution") == "confirmed_absent",
	}
}

func (f healthReconcileSubmission) subject() string {
	subject := ""
	for name, value := range map[string]string{
		"event": f.eventID, "payment": f.providerRef, "invoice": f.invoiceOperation,
	} {
		if value == "" {
			continue
		}
		if subject != "" {
			return ""
		}
		subject = name
	}
	return subject
}

func (f healthReconcileSubmission) resolutionMatches(subject string) bool {
	switch subject {
	case "event":
		return f.eventResolutionOK && !f.completeResolutionOK && !f.invoiceResolutionOK
	case "payment":
		return f.completeResolutionOK && !f.eventResolutionOK && !f.invoiceResolutionOK
	case "invoice":
		return f.invoiceResolutionOK && !f.eventResolutionOK && !f.completeResolutionOK
	default:
		return false
	}
}

func (h *Handler) applyHealthReconciliation(
	ctx context.Context, form healthReconcileSubmission,
) (invoiceQueued bool, err error) {
	subject := form.subject()
	if !form.resolutionMatches(subject) {
		return false, ErrInvalid
	}
	switch subject {
	case "event":
		return false,
			h.store.ReleasePaymentEventAfterRefundOrAccounting(ctx, form.eventID)
	case "payment":
		return false,
			h.store.ReconcileCompletePayment(ctx, form.providerRef, form.completeResolution)
	case "invoice":
		operationID, err := uuid.Parse(form.invoiceOperation)
		if err != nil {
			return true, ErrInvalid
		}
		return true,
			h.store.AuthorizeInvoiceAllowanceResend(ctx, operationID)
	default:
		panic("admin: validated unknown health reconciliation subject")
	}
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.WorkerHealth(r.Context(), h.messages)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read worker health", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	view.Pools = h.poolHealth()
	view.Disputes = readDisputes(r.Context(), h.disputes, disputeReadTimeout, h.log, h.store.OrderNumbersBySession)
	web.Render(w, r, h.log, http.StatusOK, admin.Health(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageHealth)}, &view))
}

func (h *Handler) poolHealth() []admin.PoolHealth {
	out := make([]admin.PoolHealth, 0, len(h.pools))
	for _, p := range h.pools {
		st := p.Pool.Stat()
		out = append(out, admin.PoolHealth{
			Name: p.Name, Max: st.MaxConns(), Acquired: st.AcquiredConns(),
			Idle: st.IdleConns(), Total: st.TotalConns(),
			TotalAcquires: st.AcquireCount(), EmptyAcquires: st.EmptyAcquireCount(),
			AcquireWait: st.AcquireDuration(),
		})
	}
	return out
}
