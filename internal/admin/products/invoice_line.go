package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

func (s *Store) SetProductInvoiceLine(ctx context.Context, slug string, facts invoice.LineTerms) error {
	if len(facts.Validate(ctx)) != 0 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin product invoice: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	before, err := q.LockProductInvoiceLine(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock product invoice: %w", err)
	}
	facts.Unit = invoice.ItemUnit(strings.TrimSpace(string(facts.Unit)))
	if err = q.SetProductInvoiceLine(ctx, db.SetProductInvoiceLineParams{ID: before.ID, TaxType: string(facts.TaxType), InvoiceUnit: string(facts.Unit)}); err != nil {
		return fmt.Errorf("set product invoice: %w", err)
	}
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionSetProductInvoiceLine, Table: "products", ID: uuid.NullUUID{UUID: before.ID, Valid: true},
		Before: invoiceLineState(invoice.LineTerms{TaxType: invoice.TaxType(before.TaxType), Unit: invoice.ItemUnit(before.InvoiceUnit)}),
		After:  invoiceLineState(facts),
	}); auditErr != nil {
		return auditErr
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit product invoice: %w", err)
	}
	return nil
}

// invoiceLineState is a product's invoice line as its audit row records it.
func invoiceLineState(terms invoice.LineTerms) audit.State {
	return audit.State{audit.FieldTaxType: terms.TaxType, audit.FieldInvoiceUnit: terms.Unit}
}

func (h *Handler) ProductInvoiceLine(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	facts := invoice.LineTerms{TaxType: invoice.TaxType(r.PostFormValue("tax_type")), Unit: invoice.ItemUnit(r.PostFormValue("invoice_unit"))}
	if errs := facts.Validate(r.Context()); len(errs) != 0 {
		h.rejectProductInvoiceLine(w, r, facts, errs)
		return
	}
	err := h.store.SetProductInvoiceLine(r.Context(), r.PathValue("slug"), facts)
	switch {
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "set product invoice", "error", err)
		admin.ServerError(w, r, h.log)
	default:
		slug := r.PathValue("slug")
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?invoice=1"+SectionInvoice.In()+"#sec-invoice", http.StatusSeeOther)
	}
}

func (h *Handler) rejectProductInvoiceLine(w http.ResponseWriter, r *http.Request, facts invoice.LineTerms, errs map[string]string) {
	view, err := h.product(r.Context(), r.PathValue("slug"))
	if errors.Is(err, ErrNotFound) {
		admin.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused product invoice", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.InvoiceTerms = &facts
	view.Errors = errs
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Product(frame.Page{Title: view.Name}, view))
}
