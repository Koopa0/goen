package pages

import "strconv"

// AdminCustomersView is the customer search.
//
// There was no customer page at all: credit was granted on one page, orders listed
// on another, points and tier nowhere the shop could see. Answering "what is going
// on with this customer" meant three pages and a guess.
type AdminCustomersView struct {
	// Term is what was TYPED and Searched is whether a search RAN — two fields for
	// the reason the order queue needs two: a term below the minimum is a term
	// nobody searched for, and one field made the page claim results it had never
	// looked for.
	Term     string
	Searched bool
	Rows     []AdminCustomerRow
	Notice   string
}

// AdminCustomerRow is one result.
type AdminCustomerRow struct {
	ID       string
	Email    string
	Name     string
	Since    string
	Verified bool
	Orders   int64
}

// Searching reports whether this page is showing results.
func (v AdminCustomersView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was not enough to search
// with, which the page says rather than showing an empty list.
func (v AdminCustomersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether a search found nobody.
func (v AdminCustomersView) Empty() bool { return len(v.Rows) == 0 }

// OrdersText is how many orders this customer has placed.
func (r AdminCustomerRow) OrdersText() string { return strconv.FormatInt(r.Orders, 10) }

// Href is their page.
func (r AdminCustomerRow) Href() string { return "/admin/customers/" + r.ID }

// DisplayName is their name, or their address when they gave none.
func (r AdminCustomerRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

// AdminCustomerView is one customer, whole.
type AdminCustomerView struct {
	ID       string
	Email    string
	Name     string
	Phone    string
	Since    string
	Verified bool
	Orders   int64
	// SpentCents counts COMMITTED orders only. A cancelled order is not money the
	// shop took, and counting it is the defect the committed/settled split exists
	// to stop.
	SpentCents  int64
	CreditCents int64
	Points      int64
	Recent      []AdminOrderRow
}

// OrdersText is how many orders they have placed.
func (v AdminCustomerView) OrdersText() string { return strconv.FormatInt(v.Orders, 10) }

// Spent is what they have spent on orders the shop is honouring.
func (v AdminCustomerView) Spent() string { return twd(v.SpentCents) }

// Credit is what the shop owes them.
func (v AdminCustomerView) Credit() string { return twd(v.CreditCents) }

// PointsText is their spendable points, expiry already applied.
func (v AdminCustomerView) PointsText() string { return strconv.FormatInt(v.Points, 10) }

// HasOrders reports whether they have ever bought anything.
func (v AdminCustomerView) HasOrders() bool { return len(v.Recent) > 0 }

// DisplayName is their name, or their address when they gave none.
func (v AdminCustomerView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}
