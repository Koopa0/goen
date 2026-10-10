package customers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) Warranties(ctx context.Context, term string, after ...string) (admin.WarrantiesView, error) {
	// Uppercased: a serial is typed off a label and a shift key is not a failed lookup.
	term = strings.ToUpper(strings.TrimSpace(term))
	scope := web.ScopeURL("/admin/warranty", "q", term)
	from, resumed := resume(scope, after)
	view := admin.WarrantiesView{Term: term}
	if utf8.RuneCountInString(term) < web.MinSearchRunes {
		return view, nil
	}
	view.Searched = true

	rows, err := s.q.AdminSearchWarranties(ctx, db.AdminSearchWarrantiesParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
		Term: term, RowLimit: web.PageLimit,
	})
	if err != nil {
		return admin.WarrantiesView{}, fmt.Errorf("search warranties: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminSearchWarrantiesRow) string { return r.PageCursor })
	view.Bound = bound
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].OrderID
	}
	returned, err := s.q.ReturnedOrders(ctx, ids)
	if err != nil {
		return admin.WarrantiesView{}, fmt.Errorf("read returned warranty orders: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		status := admin.FulfillmentLabel(ctx, order.FulfillmentStatus(r.FulfillmentStatus))
		if slices.Contains(returned, r.OrderID) {
			status = i18n.T(ctx, i18n.KeyStatusRefunded)
		}
		view.Rows = append(view.Rows, admin.WarrantyRow{
			Serial: r.SerialNumber, Product: r.ProductName, Label: r.VariantLabel,
			Unit: int(r.UnitNo), Order: r.OrderNumber,
			OrderStatus:   status,
			CustomerName:  r.CustomerName,
			CustomerEmail: r.CustomerEmail,
			RegisteredAt:  shoptime.Day(r.RegisteredAt),
			ExpiresOn:     shoptime.Day(r.ExpiresOn),
			InForce:       r.InForce,
		})
	}
	return view, nil
}
