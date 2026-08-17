package pages

import (
	"context"
	"fmt"

	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// WarrantyLine is one order line as the registration form sees it.
type WarrantyLine struct {
	ID    string
	Name  string
	Label string
	Slug  string
	// Note is the product's own warranty wording, shown as written.
	Note string
	// Months is the term. HasTerm is separate because a missing term is NULL:
	// products_warranty_months_sane forbids zero.
	Months  int
	HasTerm bool
	// Delivered is how many units arrived, which is the ceiling — never how many
	// were dispatched, since cover starts when the goods reach somebody.
	// Registered is how many of those already have cover.
	Delivered  int
	Registered int
}

// Remaining is how many units can still be registered.
func (l WarrantyLine) Remaining() int {
	left := l.Delivered - l.Registered
	if left < 0 {
		return 0
	}
	return left
}

// Registrable reports whether the form should offer this line.
func (l WarrantyLine) Registrable() bool { return l.HasTerm && l.Remaining() > 0 }

// NextUnit is the unit number the form submits. Units are registered in order
// and are identical, so the customer is never asked which one.
func (l WarrantyLine) NextUnit() string { return strconv.Itoa(l.Registered + 1) }

// TermText is the cover length in words.
func (l WarrantyLine) TermText(ctx context.Context) string {
	if !l.HasTerm {
		return ""
	}
	if l.Months%12 == 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyYears), l.Months/12)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyMonths), l.Months)
}

// Why explains, when a line cannot be registered, which of the reasons applies.
func (l WarrantyLine) Why(ctx context.Context) string {
	switch {
	case l.Registrable():
		return ""
	case !l.HasTerm:
		return i18n.T(ctx, i18n.KeyWarrantyNoTerm)
	case l.Delivered == 0:
		return i18n.T(ctx, i18n.KeyWarrantyNotDelivered)
	default:
		return i18n.T(ctx, i18n.KeyWarrantyAllDone)
	}
}

// WarrantyOrderView is one order's registration page.
type WarrantyOrderView struct {
	Number string
	Lines  []WarrantyLine
	Notice string
}

// AnyRegistrable reports whether the page has anything to offer.
func (v WarrantyOrderView) AnyRegistrable() bool {
	for _, l := range v.Lines {
		if l.Registrable() {
			return true
		}
	}
	return false
}

// Action is where the form posts.
func (v WarrantyOrderView) Action() string { return "/account/warranty/" + v.Number }

// Warranty is one registered unit.
type Warranty struct {
	Name         string
	Label        string
	Slug         string
	Order        string
	Unit         int
	Serial       string
	RegisteredAt string
	ExpiresOn    string
	InForce      bool
}

// State is the one word a customer scans for.
func (w Warranty) State(ctx context.Context) string {
	if w.InForce {
		return i18n.T(ctx, i18n.KeyWarrantyActive)
	}
	return i18n.T(ctx, i18n.KeyWarrantyExpired)
}

// Href is the product's page, or empty when the product is gone.
func (w Warranty) Href() string {
	if w.Slug == "" {
		return ""
	}
	return "/p/" + w.Slug
}

// WarrantyListView is the customer's registered cover.
type WarrantyListView struct {
	Rows   []Warranty
	Notice string
}

// Empty reports whether nothing is registered.
func (v WarrantyListView) Empty() bool { return len(v.Rows) == 0 }
