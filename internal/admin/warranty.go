package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Warranties looks one unit's cover up, from a serial number or an order number.
func (s *Store) Warranties(ctx context.Context, term string) (pages.AdminWarrantiesView, error) {
	// Uppercased: a serial is typed off a label and a shift key is not a failed lookup.
	term = strings.ToUpper(strings.TrimSpace(term))
	view := pages.AdminWarrantiesView{Term: term}
	if len([]rune(term)) < MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchWarranties(ctx, db.AdminSearchWarrantiesParams{
		Term: term, RowLimit: PageSize,
	})
	if err != nil {
		return pages.AdminWarrantiesView{}, fmt.Errorf("search warranties: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminWarrantyRow{
			Serial: r.SerialNumber, Product: r.ProductName, Label: r.VariantLabel,
			Unit: int(r.UnitNo), Order: r.OrderNumber,
			OrderStatus:   StatusLabel(ctx, r.FulfillmentStatus),
			CustomerName:  r.CustomerName,
			CustomerEmail: r.CustomerEmail,
			RegisteredAt:  r.RegisteredAt.Format("2006-01-02"),
			ExpiresOn:     r.ExpiresOn.Format("2006-01-02"),
			InForce:       r.InForce,
		})
	}
	return view, nil
}
