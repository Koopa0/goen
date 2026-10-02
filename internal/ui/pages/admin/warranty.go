package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// WarrantiesView is the back office's warranty lookup.
type WarrantiesView struct {
	pages.ListBound

	Term     string
	Searched bool
	Rows     []WarrantyRow
}

// WarrantyRow is one registered unit.
type WarrantyRow struct {
	Serial        string
	Product       string
	Label         string
	Unit          int
	Order         string
	OrderStatus   string
	CustomerName  string
	CustomerEmail string
	RegisteredAt  string
	ExpiresOn     string
	InForce       bool
}

// Searching reports whether this page is showing results.
func (v WarrantiesView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was too short to search.
func (v WarrantiesView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether a search found nothing.
func (v WarrantiesView) Empty() bool { return len(v.Rows) == 0 }

// UnitText is which of the line's units this is.
func (r WarrantyRow) UnitText() string { return strconv.Itoa(r.Unit) }

// SerialText is the serial number, or a dash when there is none.
func (r WarrantyRow) SerialText() string {
	if r.Serial == "" {
		return "—"
	}
	return r.Serial
}

// StateText is the one word a staff member on the phone is looking for.
func (r WarrantyRow) StateText(ctx context.Context) string {
	if r.InForce {
		return i18n.T(ctx, i18n.KeyAdminWarrantyInForce)
	}
	return i18n.T(ctx, i18n.KeyAdminWarrantyExpired)
}

// Customer is who registered it, or a note that the account is gone.
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

// OrderHref is the order this unit came from.
func (r WarrantyRow) OrderHref() string { return "/admin/orders/" + r.Order }
