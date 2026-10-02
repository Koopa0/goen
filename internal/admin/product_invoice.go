package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/layouts"
	adminpages "github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) SetProductInvoice(ctx context.Context, slug string, facts invoice.ProductFacts) error {
	if len(facts.Validate(ctx)) != 0 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin product invoice: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	before, err := q.LockProductInvoice(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock product invoice: %w", err)
	}
	facts.Unit = invoice.ItemUnit(strings.TrimSpace(string(facts.Unit)))
	if err = q.SetProductInvoice(ctx, db.SetProductInvoiceParams{ID: before.ID, TaxType: string(facts.TaxType), InvoiceUnit: string(facts.Unit)}); err != nil {
		return fmt.Errorf("set product invoice: %w", err)
	}
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionSetProductInvoice, Table: "products", ID: uuid.NullUUID{UUID: before.ID, Valid: true},
		Before: invoice.ProductFacts{TaxType: invoice.TaxType(before.TaxType), Unit: invoice.ItemUnit(before.InvoiceUnit)}, After: facts,
	}); auditErr != nil {
		return auditErr
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit product invoice: %w", err)
	}
	return nil
}

func (h *Handler) ProductInvoice(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	facts := invoice.ProductFacts{TaxType: invoice.TaxType(r.PostFormValue("tax_type")), Unit: invoice.ItemUnit(r.PostFormValue("invoice_unit"))}
	if errs := facts.Validate(r.Context()); len(errs) != 0 {
		h.rejectProductInvoice(w, r, facts, errs)
		return
	}
	err := h.store.SetProductInvoice(r.Context(), r.PathValue("slug"), facts)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "set product invoice", "error", err)
		access.ServerError(w, r, h.log)
	default:
		view, readErr := h.store.Product(r.Context(), r.PathValue("slug"))
		if readErr != nil {
			h.log.ErrorContext(r.Context(), "read saved product invoice", "error", readErr)
			access.ServerError(w, r, h.log)
			return
		}
		//nolint:gosec // slug read from the product row
		http.Redirect(w, r, view.Action()+"?ok=1#sec-invoice", http.StatusSeeOther)
	}
}

func (h *Handler) rejectProductInvoice(w http.ResponseWriter, r *http.Request, facts invoice.ProductFacts, errs map[string]string) {
	view, err := h.store.Product(r.Context(), r.PathValue("slug"))
	if errors.Is(err, ErrNotFound) {
		access.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused product invoice", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.InvoiceFacts = &facts
	view.Errors = errs
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, adminpages.ProductForm(layouts.Page{Title: view.Name}, view))
}
