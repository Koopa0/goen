package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// CompareProduct is one column of a comparison.
type CompareProduct struct {
	Slug         string
	Name         string
	Summary      string
	Brand        string
	Category     string
	PriceCents   int64
	CompareCents int64
	Rating       float64
	RatingCount  int64
	InStock      bool
	// WarrantyMonths is zero when the shop has not stated a term, which the
	// table renders as "—" rather than as "0 個月".
	WarrantyMonths int
	ImageURL       string
	ImageSrcset    string
	ImageAlt       string
}

// Price is what it costs.
func (p CompareProduct) Price() string { return twd(p.PriceCents) }

// Href is its page.
func (p CompareProduct) Href() string { return "/p/" + p.Slug }

// Stock is availability in a word.
func (p CompareProduct) Stock(ctx context.Context) string {
	if p.InStock {
		return i18n.T(ctx, i18n.KeyCompareInStock)
	}
	return i18n.T(ctx, i18n.KeySoldOut)
}

// RatingText is the score, or "—" when nobody has rated it. Zero is not a
// rating and would sort and read as the worst possible one.
func (p CompareProduct) RatingText() string {
	if p.RatingCount == 0 {
		return "—"
	}
	return strconv.FormatFloat(p.Rating, 'f', 1, 64) +
		" (" + strconv.FormatInt(p.RatingCount, 10) + ")"
}

// Warranty is the cover in words, or "—" when none is stated.
func (p CompareProduct) Warranty(ctx context.Context) string {
	switch {
	case p.WarrantyMonths == 0:
		return "—"
	case p.WarrantyMonths%12 == 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyYears), p.WarrantyMonths/12)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyMonths), p.WarrantyMonths)
	}
}

// CompareRow is one spec across every product.
type CompareRow struct {
	Label  string
	Values []string
	// SharedBy is how many of the compared products carry this label. Rows
	// everything has are the ones worth reading first, which is how the query
	// orders them.
	SharedBy int
}

// Value is the cell for column i, or "—" when that product does not state it.
//
// A missing spec is a FACT about the product — it is one the shop did not
// publish — so it renders as an absence rather than shifting the columns.
func (r CompareRow) Value(i int) string {
	if i < 0 || i >= len(r.Values) || r.Values[i] == "" {
		return "—"
	}
	return r.Values[i]
}

// Comparable reports whether every product states this spec, which is the only
// case where the row can actually be compared rather than merely read.
func (r CompareRow) Comparable(products int) bool { return r.SharedBy >= products }

// CompareView is the comparison table.
type CompareView struct {
	Products []CompareProduct
	Rows     []CompareRow
}

// Empty reports whether there is nothing to compare.
func (v CompareView) Empty() bool { return len(v.Products) == 0 }

// Enough reports whether there are at least two columns.
func (v CompareView) Enough() bool { return len(v.Products) >= 2 }

// Count is how many products are being compared.
func (v CompareView) Count() int { return len(v.Products) }

// RemoveHref is the comparison without one product, so a column can be dropped
// with a plain link — no form, no state, and the result is still shareable.
func (v CompareView) RemoveHref(slug string) string {
	var b strings.Builder
	b.WriteString("/compare")
	sep := "?"
	// Indexed rather than ranged by value: CompareProduct is 176 bytes and
	// this runs once per column per render.
	for i := range v.Products {
		if v.Products[i].Slug == slug {
			continue
		}
		b.WriteString(sep)
		b.WriteString("p=")
		b.WriteString(v.Products[i].Slug)
		sep = "&"
	}
	return b.String()
}
