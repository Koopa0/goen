package customers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/components"
)

type CustomersView struct {
	keyset.Bound

	Term     string
	Searched bool
	Rows     []CustomerRow
	Notice   components.Result
}

type CustomerRow struct {
	ID       string
	Email    string
	Name     string
	Since    string
	Verified bool
	Orders   int64
}

func (v CustomersView) Searching() bool { return v.Searched }

func (v CustomersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

func (v CustomersView) Empty() bool { return len(v.Rows) == 0 }

func (r CustomerRow) OrdersText() string { return strconv.FormatInt(r.Orders, 10) }

func (r CustomerRow) Href() string { return "/admin/customers/" + r.ID }

func (r CustomerRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

// RecentOrder is one line of the customer page's recent orders: the part of an
// order the page shows, which is not the orders desk's OrderRow.
type RecentOrder struct {
	Number       string
	Status       order.FulfillmentStatus
	StatusText   string
	StatusIntent components.Intent
	PlacedAt     string
	TotalCents   int64
}

func (o RecentOrder) Total() string { return money.TWD(o.TotalCents) }

type CustomerView struct {
	ID          string
	Email       string
	Name        string
	Phone       string
	Since       string
	Verified    bool
	Orders      int64
	SpentCents  int64
	CreditCents int64
	Points      int64
	Recent      []RecentOrder

	// WindowSpendCents is the spend over WindowDays that tiers are judged by;
	// SpentCents is lifetime. NextTierName is empty at the top tier or with no tiers.
	WindowDays       int32
	WindowSpendCents int64
	NextTierName     string
	NextTierCents    int64
}

func (v CustomerView) HasNextTier() bool { return v.NextTierName != "" }

func (v CustomerView) TierMeter() chart.MeterProps {
	return chart.MeterProps{
		Value: v.WindowSpendCents, Limit: v.NextTierCents, LimitLine: true,
		Label: money.TWD(v.WindowSpendCents) + " / " + money.TWD(v.NextTierCents),
	}
}

// TierSentence is the figure the meter draws, in words: the meter is hidden
// from assistive technology.
func (v CustomerView) TierSentence(ctx context.Context) string {
	spend := money.TWD(v.WindowSpendCents)
	if !v.HasNextTier() {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCustTierSpend), v.WindowDays, spend)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCustTierNext),
		v.WindowDays, spend, money.TWD(v.NextTierCents-v.WindowSpendCents), v.NextTierName)
}

func (v CustomerView) HasOrders() bool { return len(v.Recent) > 0 }

// CreditHref is the credit desk narrowed to this customer.
func (v CustomerView) CreditHref() string { return keyset.ScopeURL("/admin/credit", "customer", v.ID) }

// GrantCreditHref opens the same view at the grant form, where the customer's email is already filled in.
func (v CustomerView) GrantCreditHref() string { return v.CreditHref() + "#credit-email" }

func (v CustomerView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}
