package orders

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) Picking(ctx context.Context, after ...string) (admin.PickingView, error) {
	// Dispatch can change a line between the aggregate and the slips. One
	// snapshot keeps their outstanding quantities consistent without locking orders.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return admin.PickingView{}, fmt.Errorf("begin picking snapshot: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	view, err := pickingSnapshot(ctx, q, after)
	if err != nil {
		return admin.PickingView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return admin.PickingView{}, fmt.Errorf("finish picking snapshot: %w", err)
	}
	return view, nil
}

func pickingSnapshot(ctx context.Context, q *db.Queries, after []string) (admin.PickingView, error) {
	const scope = "/admin/orders/picking/slips"
	from, resumed := web.ResumeKeyset(scope, after, func(p listPosition) bool { return p.ID != uuid.Nil })
	rows, err := q.PickingSlips(ctx, db.PickingSlipsParams{
		HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit,
	})
	if err != nil {
		return admin.PickingView{}, fmt.Errorf("read picking slips: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.PickingSlipsRow) string { return r.PageCursor })
	view := admin.PickingView{Bound: bound, Slips: make([]*admin.OrderView, 0, len(rows))}
	ids := make([]uuid.UUID, 0, len(rows))
	byOrder := make(map[uuid.UUID]*admin.OrderView, len(rows))
	for i := range rows {
		r := &rows[i]
		slip := pickingSlip(r)
		view.Slips = append(view.Slips, slip)
		byOrder[r.ID] = slip
		ids = append(ids, r.ID)
	}
	lines, err := q.PickingSlipLines(ctx, ids)
	if err != nil {
		return admin.PickingView{}, fmt.Errorf("read picking slip lines: %w", err)
	}
	for i := range lines {
		l := &lines[i]
		slip := byOrder[l.OrderID]
		slip.Lines = append(slip.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Remaining,
		})
	}
	totals, err := q.PickingTotals(ctx)
	if err != nil {
		return admin.PickingView{}, fmt.Errorf("read picking totals: %w", err)
	}
	for i := range totals {
		l := &totals[i]
		view.Totals = append(view.Totals, admin.PickingLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel, Remaining: l.Remaining,
		})
	}
	return view, nil
}

func pickingSlip(r *db.PickingSlipsRow) *admin.OrderView {
	return &admin.OrderView{
		OutstandingQuantities: true,
		Number:                r.OrderNumber, PlacedAt: shoptime.Minute(r.PlacedAt), ShippingName: r.ShippingMethodName,
		CustomerNote: r.CustomerNote, Email: r.Email, Recipient: r.RecipientName, Phone: r.Phone,
		Address: pages.Delivery{
			PostalCode: r.PostalCode, City: r.City, District: r.District, Street: r.Street,
			PickupChain: pickup.Chain(r.PickupChain), PickupStoreCode: r.PickupStoreCode,
			PickupStoreName: r.PickupStoreName,
		}.Line(),
		InvoiceType: invoice.Preference(r.InvoiceType), InvoiceMobileBarcode: r.InvoiceMobileBarcode,
		InvoiceDonationCode: r.InvoiceDonationCode, InvoiceTaxID: r.InvoiceTaxID,
	}
}

func (h *Handler) PickingSlips(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Picking(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read picking slips", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Picking(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPickingSlips)}, &view))
}
