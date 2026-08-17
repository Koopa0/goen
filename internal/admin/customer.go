package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Customers searches for a customer by the start of their address or their name.
func (s *Store) Customers(ctx context.Context, term string) (pages.AdminCustomersView, error) {
	term = strings.TrimSpace(term)
	view := pages.AdminCustomersView{Term: term}
	if len([]rune(term)) < MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchCustomers(ctx, db.AdminSearchCustomersParams{
		Term: term, RowLimit: PageSize,
	})
	if err != nil {
		return pages.AdminCustomersView{}, fmt.Errorf("search customers: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminCustomerRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Since:    r.CreatedAt.Format("2006-01-02"),
			Verified: r.Verified, Orders: r.Orders,
		})
	}
	return view, nil
}

// Customer reads one customer, whole, and RECORDS that somebody looked.
func (s *Store) Customer(ctx context.Context, id string, actor uuid.NullUUID) (
	pages.AdminCustomerView, error,
) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return pages.AdminCustomerView{}, ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pages.AdminCustomerView{}, fmt.Errorf("begin customer read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.AdminCustomer(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.AdminCustomerView{}, ErrNotFound
		}
		return pages.AdminCustomerView{}, fmt.Errorf("read customer: %w", err)
	}

	orders, err := q.AdminCustomerOrders(ctx, db.AdminCustomerOrdersParams{
		UserID: uuid.NullUUID{UUID: uid, Valid: true}, Limit: PageSize,
	})
	if err != nil {
		return pages.AdminCustomerView{}, fmt.Errorf("read customer orders: %w", err)
	}

	// WHO was looked at, never what was read: audit_events outlives an erasure.
	if auditErr := auditIn(ctx, q, Event{
		Action: ActionViewCustomer, Table: "users", ID: nullableID(uid),
		Before: nil, After: map[string]any{"user_id": id},
	}); auditErr != nil {
		return pages.AdminCustomerView{}, auditErr
	}
	if err := tx.Commit(ctx); err != nil {
		return pages.AdminCustomerView{}, fmt.Errorf("commit customer read: %w", err)
	}

	view := pages.AdminCustomerView{
		ID: row.ID.String(), Email: row.Email, Name: row.FullName, Phone: row.Phone,
		Since: row.CreatedAt.Format("2006-01-02"), Verified: row.Verified,
		Orders: row.Orders, SpentCents: row.Spent,
		CreditCents: row.CreditCents, Points: row.Points,
	}
	for i := range orders {
		o := &orders[i]
		view.Recent = append(view.Recent, pages.AdminOrderRow{
			Number: o.OrderNumber, Status: o.FulfillmentStatus,
			StatusText: StatusLabel(ctx, o.FulfillmentStatus),
			PlacedAt:   o.PlacedAt.Format("2006-01-02 15:04"),
			TotalCents: o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
		})
	}
	return view, nil
}
