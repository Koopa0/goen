package admin

import (
	"strconv"

	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages"
)

// CustomersView is the customer search.
type CustomersView struct {
	pages.ListBound

	Term     string
	Searched bool
	Rows     []CustomerRow
	Notice   string
}

// CustomerRow is one result.
type CustomerRow struct {
	ID       string
	Email    string
	Name     string
	Since    string
	Verified bool
	Orders   int64
}

// Searching reports whether this page is showing results.
func (v CustomersView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was too short to search.
func (v CustomersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether a search found nobody.
func (v CustomersView) Empty() bool { return len(v.Rows) == 0 }

// OrdersText is how many orders this customer has placed.
func (r CustomerRow) OrdersText() string { return strconv.FormatInt(r.Orders, 10) }

// Href is their page.
func (r CustomerRow) Href() string { return "/admin/customers/" + r.ID }

// DisplayName is their name, or their address when they gave none.
func (r CustomerRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

// CustomerView is one customer, whole.
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
	Recent      []OrderRow
}

// OrdersText is how many orders they have placed.
func (v CustomerView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// Spent is what they have spent on orders the shop is honouring.
func (v CustomerView) Spent() string { return money.TWD(v.SpentCents) }

// Credit is what the shop owes them.
func (v CustomerView) Credit() string { return money.TWD(v.CreditCents) }

// PointsText is their spendable points, expiry already applied.
func (v CustomerView) PointsText() string { return strconv.FormatInt(v.Points, 10) }

// HasOrders reports whether they have ever bought anything.
func (v CustomerView) HasOrders() bool { return len(v.Recent) > 0 }

// DisplayName is their name, or their address when they gave none.
func (v CustomerView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}
