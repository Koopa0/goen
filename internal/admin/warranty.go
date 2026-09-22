package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Warranties looks one unit's cover up, from a serial number or an order number.
func (s *Store) Warranties(ctx context.Context, term string, after ...string) (pages.AdminWarrantiesView, error) {
	// Uppercased: a serial is typed off a label and a shift key is not a failed lookup.
	term = strings.ToUpper(strings.TrimSpace(term))
	scope := pageURL("/admin/warranty", "q", term)
	cursor := readPageCursor(scope, after)
	view := pages.AdminWarrantiesView{Term: term}
	if utf8.RuneCountInString(term) < MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchWarranties(ctx, db.AdminSearchWarrantiesParams{HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID,
		Term: term, RowLimit: PageLimit,
	})
	if err != nil {
		return pages.AdminWarrantiesView{}, fmt.Errorf("search warranties: %w", err)
	}
	rows, more := pageOf(rows, PageSize)
	last := ""
	if len(rows) > 0 {
		last = rows[len(rows)-1].PageCursor
	}
	bound := cursor.bound(scope, more, PageSize, last)
	view.ListBound = bound
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminWarrantyRow{
			Serial: r.SerialNumber, Product: r.ProductName, Label: r.VariantLabel,
			Unit: int(r.UnitNo), Order: r.OrderNumber,
			OrderStatus:   StatusLabel(ctx, pages.FulfillmentStatus(r.FulfillmentStatus)),
			CustomerName:  r.CustomerName,
			CustomerEmail: r.CustomerEmail,
			RegisteredAt:  shoptime.Day(r.RegisteredAt),
			ExpiresOn:     shoptime.Day(r.ExpiresOn),
			InForce:       r.InForce,
		})
	}
	return view, nil
}
