// Package warranties is the staff search over warranty registrations by serial
// or order number; it holds the /admin/warranties page, its query and its rows;
// it does not hold recording cover, which is package warranty.
package warranties

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/web/formtext"
)

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("warranties: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// position is a reader's place in a list. The query builds it as PageCursor,
// so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}

func resume(scope string, after []string) (position, bool) {
	return keyset.Resume(scope, after, func(p position) bool { return p.ID != uuid.Nil })
}

func (s *Store) Warranties(ctx context.Context, term string, after ...string) (View, error) {
	// Uppercased: a serial is typed off a label and a shift key is not a failed lookup.
	term = strings.ToUpper(strings.TrimSpace(term))
	scope := keyset.ScopeURL("/admin/warranties", "q", term)
	from, resumed := resume(scope, after)
	view := View{Term: term}
	if utf8.RuneCountInString(term) < formtext.MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchWarranties(ctx, db.AdminSearchWarrantiesParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
		Term: term, RowLimit: keyset.PageLimit,
	})
	if err != nil {
		return View{}, fmt.Errorf("search warranties: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminSearchWarrantiesRow) string { return r.PageCursor })
	view.Bound = bound
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].OrderID
	}
	returned, err := s.q.ReturnedOrders(ctx, ids)
	if err != nil {
		return View{}, fmt.Errorf("read returned warranty orders: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		status := order.FulfillmentStatus(r.FulfillmentStatus).Label(ctx)
		gone := slices.Contains(returned, r.OrderID)
		if gone {
			status = i18n.T(ctx, i18n.KeyStatusRefunded)
		}
		view.Rows = append(view.Rows, Row{
			Serial: r.SerialNumber, Product: r.ProductName, Label: r.VariantLabel,
			Unit: int(r.UnitNo), Order: r.OrderNumber,
			OrderStatus:   status,
			CustomerName:  r.CustomerName,
			CustomerID:    r.CustomerID,
			CustomerEmail: r.CustomerEmail,
			RegisteredAt:  shoptime.Day(r.RegisteredAt),
			ExpiresOn:     shoptime.Day(r.ExpiresOn),
			InForce:       r.InForce && !gone,
		})
	}
	return view, nil
}
