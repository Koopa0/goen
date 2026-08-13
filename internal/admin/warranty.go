package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Warranties looks one unit's cover up, from a serial number or an order number.
//
// The customer has been able to register a warranty since the day the feature
// shipped and NOBODY AT THE SHOP could read one. /warranty promises a registered
// unit is collected and repaired at the shop's expense, so the moment that
// customer rings up, the only person who can see the registration is the person
// making the claim — a promise kept on trust, or not kept.
//
// Nothing is listed until somebody searches, the /admin/customers rule and for
// the same reason: these rows carry a customer's name beside what they own, and
// a back office that opens on that list invites reading it.
//
// The READ is not audited, and that is a decision rather than an omission.
// /admin/customers records its reads because that page IS somebody's personal
// data and can be reached by browsing to it. This one is reached by typing a
// serial number the customer has just read out, shows less than
// /admin/orders/{number} already shows unaudited, and cannot be walked: there is
// no query here that returns a row without being handed the exact string.
func (s *Store) Warranties(ctx context.Context, term string) (pages.AdminWarrantiesView, error) {
	// Uppercased because a serial is typed off a label and a shift key is not a
	// failed lookup — the same reason the 店號 field uppercases on Trim. Order
	// numbers are already uppercase, so this costs that path nothing.
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
