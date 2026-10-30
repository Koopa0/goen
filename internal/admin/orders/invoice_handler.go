package orders

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/web/formtext"
)

// IssueInvoice files the order's uniform invoice.
func (h *Handler) IssueInvoice(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	if !order.ValidNumber(number) {
		http.NotFound(w, r)
		return
	}
	err := h.store.IssueInvoice(r.Context(), number)
	if errors.Is(err, issuer.ErrNotFound) {
		// The number is well formed and names no order.
		http.NotFound(w, r)
		return
	}
	h.answerInvoice(w, r, number, "issue invoice", err, issueNotice)
}

// invoiceOutcome is where an invoice press sends the staff member and what the
// log says of it: Error for what needs a person, Warn for a refusal, and no
// line when the notice says it all.
type invoiceOutcome struct {
	Query string
	Log   string
	Level slog.Level
}

// answerInvoice redirects to the order page with the notice that notice picks
// for err, and answers a server error for an error no notice describes.
func (h *Handler) answerInvoice(
	w http.ResponseWriter, r *http.Request, number, operation string, err error,
	notice func(error) (invoiceOutcome, bool),
) {
	outcome, ok := notice(err)
	if !ok {
		h.log.ErrorContext(r.Context(), operation, "order", number, "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	if outcome.Log != "" {
		h.log.Log(r.Context(), outcome.Level, outcome.Log, "order", number, "error", err)
	}
	http.Redirect(w, r, "/admin/orders/"+url.PathEscape(number)+outcome.Query, http.StatusSeeOther)
}

// issueNotice is the outcome that selects the order page notice for what
// IssueInvoice returned, and false for an error no notice describes.
func issueNotice(err error) (invoiceOutcome, bool) {
	switch {
	case err == nil:
		return invoiceOutcome{Query: "?invoiced=1"}, true
	case errors.Is(err, issuer.ErrAlreadyIssued):
		return invoiceOutcome{Query: "?hasinvoice=1"}, true
	case errors.Is(err, issuer.ErrDisabled):
		return invoiceOutcome{Query: "?invoicingoff=1"}, true
	case errors.Is(err, ErrInvoiceRefused):
		return invoiceOutcome{Query: "?refused=1", Log: "invoice refused", Level: slog.LevelWarn}, true
	case errors.Is(err, issuer.ErrRejected):
		// The provider's own reason, logged in full because it names the field to
		// fix; the page says one thing, because nobody can act on an RtnCode.
		return invoiceOutcome{
			Query: "?invoicefailed=1", Log: "the e-invoice provider refused the invoice", Level: slog.LevelError,
		}, true
	case errors.Is(err, issuer.ErrPending):
		return invoiceOutcome{Query: "?invoicepending=1"}, true
	}
	return invoiceOutcome{}, false
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
	err := h.store.VoidInvoice(r.Context(), number, r.PostFormValue("reason"))
	h.answerInvoice(w, r, number, "void invoice", err, voidNotice)
}

// voidNotice is the outcome that selects the order page notice for what
// VoidInvoice returned, and false for an error no notice describes.
func voidNotice(err error) (invoiceOutcome, bool) {
	switch {
	case err == nil:
		return invoiceOutcome{Query: "?voided=1"}, true
	case errors.Is(err, issuer.ErrReason):
		return invoiceOutcome{Query: "?voidreason=1"}, true
	case errors.Is(err, issuer.ErrNotFound):
		return invoiceOutcome{Query: "?noinvoice=1"}, true
	case errors.Is(err, issuer.ErrPending):
		return invoiceOutcome{Query: "?invoicepending=1"}, true
	case errors.Is(err, issuer.ErrRejected):
		// invoicefailed names 統編 and carrier codes. A void form collects a
		// reason; the provider refusal belongs on 綠界, not checkout tax ids.
		return invoiceOutcome{
			Query: "?voidfailed=1", Log: "the e-invoice provider refused the void", Level: slog.LevelError,
		}, true
	case errors.Is(err, issuer.ErrDisabled):
		return invoiceOutcome{Query: "?invoicingoff=1"}, true
	case errors.Is(err, ErrInvoiceRefused):
		return invoiceOutcome{Query: "?refused=1", Log: "invoice void refused", Level: slog.LevelWarn}, true
	}
	return invoiceOutcome{}, false
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
	operationID, operationErr := uuid.Parse(r.PostFormValue("operation_id"))
	if operationErr != nil || operationID == uuid.Nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}

	err := h.store.FileAllowance(r.Context(), number, operationID)
	h.answerInvoice(w, r, number, "file invoice allowance", err, allowanceNotice)
}

// allowanceNotice is the outcome that selects the order page notice for what
// FileAllowance returned, and false for an error no notice describes.
func allowanceNotice(err error) (invoiceOutcome, bool) {
	switch {
	case err == nil:
		return invoiceOutcome{Query: "?allowed=1"}, true
	case errors.Is(err, issuer.ErrNotFound):
		return invoiceOutcome{Query: "?noinvoice=1"}, true
	case errors.Is(err, issuer.ErrClaimed):
		return invoiceOutcome{Query: "?allowclaimed=1"}, true
	case errors.Is(err, issuer.ErrAwaitingBuyer):
		return invoiceOutcome{Query: "?allowsent=1"}, true
	case errors.Is(err, issuer.ErrPending):
		return invoiceOutcome{Query: "?invoicepending=1"}, true
	case errors.Is(err, issuer.ErrTooMuch):
		return invoiceOutcome{Query: "?allowtoomuch=1"}, true
	case errors.Is(err, issuer.ErrRejected):
		// invoicefailed names 統編 and carrier codes, which is right for ISSUING.
		// An allowance form does not collect those fields.
		return invoiceOutcome{
			Query: "?allowfailed=1", Log: "the e-invoice provider refused the allowance", Level: slog.LevelError,
		}, true
	case errors.Is(err, issuer.ErrDisabled):
		return invoiceOutcome{Query: "?invoicingoff=1"}, true
	case errors.Is(err, ErrInvoiceRefused):
		return invoiceOutcome{Query: "?refused=1", Log: "invoice allowance refused", Level: slog.LevelWarn}, true
	}
	return invoiceOutcome{}, false
}
