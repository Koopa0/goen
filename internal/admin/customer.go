package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) Customers(ctx context.Context, term string, after ...string) (admin.CustomersView, error) {
	term = strings.TrimSpace(term)
	scope := web.ScopeURL("/admin/customers", "q", term)
	cursor := readPageCursor(scope, after)
	view := admin.CustomersView{Term: term}
	if utf8.RuneCountInString(term) < MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchCustomers(ctx, db.AdminSearchCustomersParams{HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
		EscapedTerm: catalog.EscapeLike(term), RowLimit: PageLimit,
	})
	if err != nil {
		return admin.CustomersView{}, fmt.Errorf("search customers: %w", err)
	}
	rows, bound := pageBound(cursor, scope, rows, PageSize, func(r *db.AdminSearchCustomersRow) string { return r.PageCursor })
	view.ListBound = bound
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.CustomerRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Since:    shoptime.Day(r.CreatedAt),
			Verified: r.Verified, Orders: r.Orders,
		})
	}
	return view, nil
}

// Customer reads one customer, whole, and RECORDS that somebody looked.
func (s *Store) Customer(ctx context.Context, id string, actor uuid.NullUUID) (
	admin.CustomerView, error,
) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return admin.CustomerView{}, ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return admin.CustomerView{}, fmt.Errorf("begin customer read: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.AdminCustomer(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.CustomerView{}, ErrNotFound
		}
		return admin.CustomerView{}, fmt.Errorf("read customer: %w", err)
	}

	orders, err := q.AdminCustomerOrders(ctx, db.AdminCustomerOrdersParams{
		UserID: uuid.NullUUID{UUID: uid, Valid: true}, Limit: PageSize,
	})
	if err != nil {
		return admin.CustomerView{}, fmt.Errorf("read customer orders: %w", err)
	}

	// WHO was looked at, never what was read: audit_events outlives an erasure.
	if auditErr := auditIn(ctx, q, Event{
		Action: actionViewCustomer, Table: "users", ID: nullableID(uid),
		Before: nil, After: map[string]any{"user_id": id},
	}); auditErr != nil {
		return admin.CustomerView{}, auditErr
	}
	if err := tx.Commit(ctx); err != nil {
		return admin.CustomerView{}, fmt.Errorf("commit customer read: %w", err)
	}

	view := admin.CustomerView{
		ID: row.ID.String(), Email: row.Email, Name: row.FullName, Phone: row.Phone,
		Since: shoptime.Day(row.CreatedAt), Verified: row.Verified,
		Orders: row.Orders, SpentCents: row.Spent,
		CreditCents: row.CreditCents, Points: row.Points,
	}
	for i := range orders {
		o := &orders[i]
		fulfillment := pages.FulfillmentStatus(o.FulfillmentStatus)
		view.Recent = append(view.Recent, admin.OrderRow{
			Number: o.OrderNumber, Status: fulfillment,
			StatusText: FundedStatusLabel(ctx, fulfillment, o.Committed, o.OwedCents),
			PlacedAt:   shoptime.Minute(o.PlacedAt),
			TotalCents: o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
		})
	}
	return view, nil
}
