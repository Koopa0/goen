package orders

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order"
)

// IssueInvoice files the order's uniform invoice.
func (h *Handler) IssueInvoice(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.IssueInvoice(r.Context(), number)
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoiced=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrAlreadyIssued):
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?hasinvoice=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrDisabled):
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicingoff=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvoiceRefused):
		h.log.WarnContext(r.Context(), "invoice refused", "order", number, "error", err)
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?refused=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrRejected):
		// The provider's own reason, logged in full because it names the field to
		// fix; the page says one thing, because nobody can act on an RtnCode.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the invoice",
			"order", number, "error", err)
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicefailed=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrPending):
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?invoicepending=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "issue invoice", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}

// VoidInvoice voids a wrong invoice so a correct one can be issued in its
// place: a uniform invoice cannot be edited, which invoice_documents_guard enforces.
func (h *Handler) VoidInvoice(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	orderPage := "/admin/orders/" + url.PathEscape(number)
	err := h.store.VoidInvoice(r.Context(), number, r.PostFormValue("reason"))
	switch {
	case err == nil:
		http.Redirect(w, r, orderPage+"?voided=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrReason):
		http.Redirect(w, r, orderPage+"?voidreason=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrNotFound):
		http.Redirect(w, r, orderPage+"?noinvoice=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrPending):
		http.Redirect(w, r, orderPage+"?invoicepending=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrRejected):
		// invoicefailed names 統編 and carrier codes. A void form collects a
		// reason; the provider refusal belongs on 綠界, not checkout tax ids.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the void",
			"order", number, "error", err)
		http.Redirect(w, r, orderPage+"?voidfailed=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrDisabled):
		http.Redirect(w, r, orderPage+"?invoicingoff=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvoiceRefused):
		h.log.WarnContext(r.Context(), "invoice void refused", "order", number, "error", err)
		http.Redirect(w, r, orderPage+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "void invoice", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}

// FileAllowance files an allowance. A refund leaves the 統一發票 recording a sale that partly did not happen, and
// a 折讓 is the correction the 財政部 accepts for it — a void is for an invoice
// that should not exist, an allowance for one that should exist for less. The
// amount is derived from settled refunds and prior filed allowances inside the
// database claim. The form carries no money for an operator to override.
func (h *Handler) FileAllowance(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	orderPage := "/admin/orders/" + url.PathEscape(number)
	operationID, operationErr := uuid.Parse(r.PostFormValue("operation_id"))
	if operationErr != nil || operationID == uuid.Nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	err := h.store.FileAllowance(r.Context(), number, operationID)
	switch {
	case err == nil:
		http.Redirect(w, r, orderPage+"?allowed=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrNotFound):
		http.Redirect(w, r, orderPage+"?noinvoice=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrClaimed):
		http.Redirect(w, r, orderPage+"?allowclaimed=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrAwaitingBuyer):
		http.Redirect(w, r, orderPage+"?allowsent=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrPending):
		http.Redirect(w, r, orderPage+"?invoicepending=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrTooMuch):
		http.Redirect(w, r, orderPage+"?allowtoomuch=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrRejected):
		// invoicefailed names 統編 and carrier codes, which is right for ISSUING.
		// An allowance form does not collect those fields.
		h.log.ErrorContext(r.Context(), "the e-invoice provider refused the allowance",
			"order", number, "error", err)
		http.Redirect(w, r, orderPage+"?allowfailed=1", http.StatusSeeOther)
	case errors.Is(err, issuer.ErrDisabled):
		http.Redirect(w, r, orderPage+"?invoicingoff=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvoiceRefused):
		h.log.WarnContext(r.Context(), "invoice allowance refused", "order", number, "error", err)
		http.Redirect(w, r, orderPage+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "file invoice allowance", "order", number, "error", err)
		access.ServerError(w, r, h.log)
	}
}
