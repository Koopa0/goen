// Package customers is the back office's customer lookup: search by name or
// address, and one customer's page.
package customers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
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
	return keyset.Resume(scope, after, func(p position) bool { return p.ID != uuid.Nil })
}

func (s *Store) Search(ctx context.Context, term string, after ...string) (CustomersView, error) {
	term = strings.TrimSpace(term)
	scope := keyset.ScopeURL("/admin/customers", "q", term)
	from, resumed := resume(scope, after)
	view := CustomersView{Term: term}
	if utf8.RuneCountInString(term) < formtext.MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchCustomers(ctx, db.AdminSearchCustomersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
		EscapedTerm: db.EscapeLike(term), RowLimit: keyset.PageLimit,
	})
	if err != nil {
		return CustomersView{}, fmt.Errorf("search customers: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminSearchCustomersRow) string { return r.PageCursor })
	view.Bound = bound
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, CustomerRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Since:    shoptime.Day(r.CreatedAt),
			Verified: r.Verified, Orders: r.Orders,
		})
	}
	return view, nil
}

// Profile reads one customer, whole, and RECORDS that somebody looked.
func (s *Store) Profile(ctx context.Context, id string) (CustomerView, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return CustomerView{}, ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CustomerView{}, fmt.Errorf("begin customer read: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.AdminCustomer(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CustomerView{}, ErrNotFound
		}
		return CustomerView{}, fmt.Errorf("read customer: %w", err)
	}

	standing, err := q.MemberStanding(ctx, db.MemberStandingParams{
		UserID: uid, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return CustomerView{}, fmt.Errorf("read customer standing: %w", err)
	}

	orders, err := q.AdminCustomerOrders(ctx, db.AdminCustomerOrdersParams{
		UserID: uuid.NullUUID{UUID: uid, Valid: true}, Limit: keyset.PageSize,
	})
	if err != nil {
		return CustomerView{}, fmt.Errorf("read customer orders: %w", err)
	}

	ids := make([]uuid.UUID, len(orders))
	for i := range orders {
		ids[i] = orders[i].ID
	}
	returned, err := q.ReturnedOrders(ctx, ids)
	if err != nil {
		return CustomerView{}, fmt.Errorf("read returned customer orders: %w", err)
	}

	// WHO was looked at, never what was read: audit_events outlives an erasure.
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionViewCustomer, Table: "users", ID: audit.EntityID(uid),
		Before: nil, After: audit.State{audit.FieldUserID: id},
	}); auditErr != nil {
		return CustomerView{}, auditErr
	}
	if err := tx.Commit(ctx); err != nil {
		return CustomerView{}, fmt.Errorf("commit customer read: %w", err)
	}

	view := CustomerView{
		ID: row.ID.String(), Email: row.Email, Name: row.FullName, Phone: row.Phone,
		Since: shoptime.Day(row.CreatedAt), Verified: row.Verified,
		Orders: row.Orders, SpentCents: row.Spent,
		CreditCents: row.CreditCents, Points: row.Points,
		WindowDays: standing.WindowDays, WindowSpendCents: standing.SpendCents, NextTierName: standing.NextName,
		NextTierCents: standing.SpendCents + standing.NextNeedsCents,
	}
	for i := range orders {
		view.Recent = append(view.Recent, recentOrderRow(ctx, &orders[i], slices.Contains(returned, orders[i].ID)))
	}
	return view, nil
}

func recentOrderRow(ctx context.Context, o *db.AdminCustomerOrdersRow, returned bool) RecentOrder {
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	text := fulfillment.FundedLabel(ctx, o.Committed, o.OwedCents)
	intent := widget.FundedFulfillmentIntent(fulfillment, o.Committed, o.OwedCents)
	if returned {
		text, intent = i18n.T(ctx, i18n.KeyStatusRefunded), widget.IntentNeutral
	}
	return RecentOrder{
		Number:       o.OrderNumber,
		Status:       fulfillment,
		StatusText:   text,
		StatusIntent: intent,
		PlacedAt:     shoptime.Minute(o.PlacedAt),
		TotalCents:   o.TotalCents,
	}
}
