package warranties

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

type View struct {
	keyset.Bound

	Term     string
	Searched bool
	Rows     []Row
}

type Row struct {
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

func (v View) Searching() bool { return v.Searched }

func (v View) TermTooShort() bool { return v.Term != "" && !v.Searched }

func (v View) Empty() bool { return len(v.Rows) == 0 }

func (r Row) UnitText() string { return strconv.Itoa(r.Unit) }

func (r Row) SerialText() string {
	if r.Serial == "" {
		return "—"
	}
	return r.Serial
}

func (r Row) StateText(ctx context.Context) string {
	if r.InForce {
		return i18n.T(ctx, i18n.KeyAdminWarrantyInForce)
	}
	return i18n.T(ctx, i18n.KeyAdminWarrantyExpired)
}

func (r Row) Customer(ctx context.Context) string {
	switch {
	case r.CustomerName != "":
		return r.CustomerName
	case r.CustomerEmail != "":
		return r.CustomerEmail
	default:
		return i18n.T(ctx, i18n.KeyAdminWarrantyErasedAccount)
	}
}

func (r Row) OrderHref() string { return "/admin/orders/" + r.Order }

func (r Row) CustomerHref() string { return "/admin/customers/" + r.CustomerID }
