package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

type WarrantiesView struct {
	web.Bound

	Term     string
	Searched bool
	Rows     []WarrantyRow
}

type WarrantyRow struct {
	Serial        string
	Product       string
	Label         string
	Unit          int
	Order         string
	OrderStatus   string
	CustomerName  string
	CustomerID    string
	CustomerEmail string
	RegisteredAt  string
	ExpiresOn     string
	InForce       bool
}

func (v WarrantiesView) Searching() bool { return v.Searched }

func (v WarrantiesView) TermTooShort() bool { return v.Term != "" && !v.Searched }

func (v WarrantiesView) Empty() bool { return len(v.Rows) == 0 }

func (r WarrantyRow) UnitText() string { return strconv.Itoa(r.Unit) }

func (r WarrantyRow) SerialText() string {
	if r.Serial == "" {
		return "—"
	}
	return r.Serial
}

func (r WarrantyRow) StateText(ctx context.Context) string {
	if r.InForce {
		return i18n.T(ctx, i18n.KeyAdminWarrantyInForce)
	}
	return i18n.T(ctx, i18n.KeyAdminWarrantyExpired)
}

func (r WarrantyRow) Customer(ctx context.Context) string {
	switch {
	case r.CustomerName != "":
		return r.CustomerName
	case r.CustomerEmail != "":
		return r.CustomerEmail
	default:
		return i18n.T(ctx, i18n.KeyAdminWarrantyErasedAccount)
	}
}

func (r WarrantyRow) OrderHref() string { return "/admin/orders/" + r.Order }

func (r WarrantyRow) CustomerHref() string { return "/admin/customers/" + r.CustomerID }
