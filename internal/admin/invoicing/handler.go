package invoicing

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/ordernumber"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("invoicing: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("POST /admin/orders/{number}/invoice", ac.RequireStaff(h.Issue))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/void", ac.RequireStaff(h.Void))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/allowance", ac.RequireStaff(h.Allow))
}

func (h *Handler) Issue(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !ordernumber.Valid(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.Issue(r.Context(), number)
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

// Void voids a wrong invoice so a correct one can be issued in its
// place: a uniform invoice cannot be edited, which invoice_documents_guard enforces.
func (h *Handler) Void(w http.ResponseWriter, r *http.Request) {
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
	err := h.store.Void(r.Context(), number, r.PostFormValue("reason"))
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

// Allow files an allowance. A refund leaves the 統一發票 recording a sale that partly did not happen, and
// a 折讓 is the correction the 財政部 accepts for it — a void is for an invoice
// that should not exist, an allowance for one that should exist for less. The
// amount is derived from settled refunds and prior filed allowances inside the
// database claim. The form carries no money for an operator to override.
func (h *Handler) Allow(w http.ResponseWriter, r *http.Request) {
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

	err := h.store.Allow(r.Context(), number, operationID)
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
