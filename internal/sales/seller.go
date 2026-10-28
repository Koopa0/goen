package sales

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

// RecentSellers is how many best sellers a [Recent] lists.
const RecentSellers = 5

// Seller is a product and what it sold over a run of shop days.
type Seller struct {
	Slug         string
	Name         string
	Brand        string
	Units        int64
	RevenueCents int64
}

// TopUnits is the most units any of sellers sold: the length a bar of a best
// seller list is a share of.
func TopUnits(sellers []Seller) int64 {
	var top int64
	for _, s := range sellers {
		top = max(top, s.Units)
	}
	return top
}

// Revenue formats product gross before order-level adjustments; a list labels
// it apart from total revenue.
func (s Seller) Revenue() string { return money.TWD(s.RevenueCents) }

// Facts is the line under the name: the brand, what was sold and for how much.
func (s Seller) Facts(ctx context.Context) string {
	var facts []string
	if s.Brand != "" {
		facts = append(facts, s.Brand)
	}
	return strings.Join(append(facts,
		i18n.Count(ctx, i18n.KeyAdminRepUnits, s.Units, s.UnitsText()),
		fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepGross), s.Revenue())), " · ")
}

func (s Seller) UnitsText() string { return strconv.FormatInt(s.Units, 10) }

// Href is the product's page in the back office.
func (s Seller) Href() string { return "/admin/products/" + s.Slug }
