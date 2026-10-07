// Package customers is the back office's customer lookup: search by name or
// address, one customer's page, and the warranty search that finds a customer by
// serial number.
package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

var ErrNotFound = errors.New("customers: not found")

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("customers: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// position is a reader's place in a list. The queries build it as PageCursor,
// so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}

func resume(scope string, after []string) (position, bool) {
	return web.ResumeKeyset(scope, after, func(p position) bool { return p.ID != uuid.Nil })
}

func (s *Store) Search(ctx context.Context, term string, after ...string) (admin.CustomersView, error) {
	term = strings.TrimSpace(term)
	scope := web.ScopeURL("/admin/customers", "q", term)
	from, resumed := resume(scope, after)
	view := admin.CustomersView{Term: term}
	if utf8.RuneCountInString(term) < web.MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchCustomers(ctx, db.AdminSearchCustomersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
		EscapedTerm: catalog.EscapeLike(term), RowLimit: web.PageLimit,
	})
	if err != nil {
		return admin.CustomersView{}, fmt.Errorf("search customers: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminSearchCustomersRow) string { return r.PageCursor })
	view.Bound = bound
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

// Profile reads one customer, whole, and RECORDS that somebody looked.
func (s *Store) Profile(ctx context.Context, id string) (admin.CustomerView, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return admin.CustomerView{}, ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return admin.CustomerView{}, fmt.Errorf("begin customer read: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.AdminCustomer(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.CustomerView{}, ErrNotFound
		}
		return admin.CustomerView{}, fmt.Errorf("read customer: %w", err)
	}

	standing, err := q.MemberStanding(ctx, db.MemberStandingParams{
		UserID: uid, WindowDays: loyalty.MembershipWindowDays, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return admin.CustomerView{}, fmt.Errorf("read customer standing: %w", err)
	}

	orders, err := q.AdminCustomerOrders(ctx, db.AdminCustomerOrdersParams{
		UserID: uuid.NullUUID{UUID: uid, Valid: true}, Limit: web.PageSize,
	})
	if err != nil {
		return admin.CustomerView{}, fmt.Errorf("read customer orders: %w", err)
	}

	// WHO was looked at, never what was read: audit_events outlives an erasure.
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionViewCustomer, Table: "users", ID: audit.EntityID(uid),
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
		WindowDays: loyalty.MembershipWindowDays, WindowSpendCents: standing.SpendCents, NextTierName: standing.NextName,
		NextTierCents: standing.SpendCents + standing.NextNeedsCents,
	}
	for i := range orders {
		view.Recent = append(view.Recent, recentOrderRow(ctx, &orders[i]))
	}
	return view, nil
}

func recentOrderRow(ctx context.Context, o *db.AdminCustomerOrdersRow) admin.OrderRow {
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	return admin.OrderRow{
		Number:       o.OrderNumber,
		Status:       fulfillment,
		StatusText:   admin.FundedFulfillmentLabel(ctx, fulfillment, o.Committed, o.OwedCents),
		StatusIntent: admin.FundedFulfillmentIntent(fulfillment, o.Committed, o.OwedCents),
		PlacedAt:     shoptime.Minute(o.PlacedAt),
		TotalCents:   o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
	}
}
