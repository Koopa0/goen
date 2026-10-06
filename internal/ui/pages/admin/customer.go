package admin

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

type CustomersView struct {
	web.Bound

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

func (v CustomerView) HasOrders() bool { return len(v.Recent) > 0 }

func (v CustomerView) DisplayName() string {
	if v.Name == "" {
		return v.Email
	}
	return v.Name
}
