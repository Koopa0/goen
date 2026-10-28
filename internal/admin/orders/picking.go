package orders

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

// Picking is the slips of every order with something left to ship, a page at a time.
func (s *Store) Picking(ctx context.Context, after string) (PickingView, error) {
	return s.pickingView(ctx, after, nil)
}

var errNoSelection = errors.New("orders: a selection of slips needs one to a page of orders")

// PickingOf is the slips and totals of the named orders only. There is no
// cursor and no next page: the numbers are the whole request. An order among
// them with nothing left to ship is not listed, and the view counts it.
func (s *Store) PickingOf(ctx context.Context, numbers []string) (PickingView, error) {
	if len(numbers) == 0 || len(numbers) > keyset.PageSize {
		return PickingView{}, errNoSelection
	}
	view, err := s.pickingView(ctx, "", numbers)
	if err != nil {
		return PickingView{}, err
	}
	view.Selection = &PickingSelection{Asked: len(numbers), Listed: len(view.Slips)}
	return view, nil
}

func (s *Store) pickingView(ctx context.Context, after string, numbers []string) (PickingView, error) {
	// Dispatch can change a line between the aggregate and the slips. One
	// snapshot keeps their outstanding quantities consistent without locking orders.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return PickingView{}, fmt.Errorf("begin picking snapshot: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	view, err := pickingSnapshot(ctx, q, after, numbers)
	if err != nil {
		return PickingView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PickingView{}, fmt.Errorf("finish picking snapshot: %w", err)
	}
	return view, nil
}

// pickingSnapshot reads the slips and the totals. With numbers it reads those
// orders alone: no scope, no paging.
func pickingSnapshot(ctx context.Context, q *db.Queries, after string, numbers []string) (PickingView, error) {
	const scope = "/admin/orders/picking/slips"
	selected := len(numbers) > 0
	var from listPosition
	var resumed bool
	if !selected {
		from, resumed = keyset.Resume(scope, after, func(p listPosition) bool { return p.ID != uuid.Nil })
		// An empty list, not nil: pgx sends nil as NULL.
		numbers = []string{}
	}
	rows, err := q.PickingSlips(ctx, db.PickingSlipsParams{
		HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: keyset.PageLimit,
		Locale: i18n.FromContext(ctx).Tag(), Numbers: numbers,
	})
	if err != nil {
		return PickingView{}, fmt.Errorf("read picking slips: %w", err)
	}
	var bound keyset.Bound
	if !selected {
		rows, bound = keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.PickingSlipsRow) string { return r.PageCursor })
	}
	view := PickingView{Bound: bound, Slips: make([]*OrderView, 0, len(rows))}
	ids := make([]uuid.UUID, 0, len(rows))
	byOrder := make(map[uuid.UUID]*OrderView, len(rows))
	for i := range rows {
		r := &rows[i]
		slip := pickingSlip(r)
		view.Slips = append(view.Slips, slip)
		byOrder[r.ID] = slip
		ids = append(ids, r.ID)
	}
	lines, err := q.PickingSlipLines(ctx, ids)
	if err != nil {
		return PickingView{}, fmt.Errorf("read picking slip lines: %w", err)
	}
	for i := range lines {
		l := &lines[i]
		slip := byOrder[l.OrderID]
		slip.Lines = append(slip.Lines, Line{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Remaining,
		})
	}
	totals, err := q.PickingTotals(ctx, numbers)
	if err != nil {
		return PickingView{}, fmt.Errorf("read picking totals: %w", err)
	}
	for i := range totals {
		l := &totals[i]
		view.Totals = append(view.Totals, PickingLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel, Remaining: l.Remaining,
		})
	}
	return view, nil
}

func pickingSlip(r *db.PickingSlipsRow) *OrderView {
	to := delivery.Details{
		PostalCode: r.PostalCode, City: r.City, District: r.District, Street: r.Street,
		PickupChain: shipping.Chain(r.PickupChain), PickupStoreCode: r.PickupStoreCode,
		PickupStoreName: r.PickupStoreName,
	}
	return &OrderView{
		OutstandingQuantities: true,
		Number:                r.OrderNumber, PlacedAt: shoptime.Minute(r.PlacedAt), ShippingName: r.ShippingMethodName,
		CustomerNote: r.CustomerNote, Email: r.Email, Recipient: r.RecipientName, Phone: r.Phone,
		Address:     to.Line(),
		InvoiceType: invoice.Preference(r.InvoiceType), InvoiceMobileBarcode: r.InvoiceMobileBarcode,
		InvoiceDonationCode: r.InvoiceDonationCode, InvoiceTaxID: r.InvoiceTaxID,
	}
}

// PickingSlips prints the slips of every order with something left to ship, a
// page at a time; or, when the request names orders, those orders alone. A
// request that asks for a selection and names none is sent back to the tab
// rather than answered with the whole queue.
func (h *Handler) PickingSlips(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var view PickingView
	var err error
	if query.Has(selectionParam) || query.Has("order") {
		var numbers []string
		numbers, err = selectedNumbers(query["order"])
		if err != nil {
			http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
			return
		}
		if len(numbers) == 0 {
			http.Redirect(w, r, "/admin/orders?status=picking&nosel=1", http.StatusSeeOther)
			return
		}
		view, err = h.store.PickingOf(r.Context(), numbers)
	} else {
		view, err = h.store.Picking(r.Context(), query.Get(keyset.Param))
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read picking slips", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, Picking(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPickingSlips)}, &view))
}
