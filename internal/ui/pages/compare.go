package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// MinCompare and MaxCompare bound how many products a comparison holds. The
// comparison is the URL, so these are the only place either number is written.
const (
	MinCompare = 2
	MaxCompare = 4
)

// CompareCandidate is a search result that can be added to the comparison.
type CompareCandidate struct {
	Slug  string
	Name  string
	Brand string
}

// CompareProduct is one column of a comparison.
type CompareProduct struct {
	Slug           string
	Name           string
	Summary        string
	Brand          string
	Category       string
	PriceCents     int64
	CompareCents   int64
	Rating         float64
	RatingCount    int64
	InStock        bool
	WarrantyMonths int
	ImageURL       string
	ImageSrcset    string
	ImageAlt       string
}

// Price is what it costs.
func (p CompareProduct) Price() string { return twd(p.PriceCents) }

// Stock is availability in a word.
func (p CompareProduct) Stock(ctx context.Context) string {
	if p.InStock {
		return i18n.T(ctx, i18n.KeyInStock)
	}
	return i18n.T(ctx, i18n.KeySoldOut)
}

// RatingText is the score, or "—" when nobody has rated it.
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
	Label    string
	Values   []string
	SharedBy int
}

// Value is the cell for column i, or "—" when that product does not state it.
func (r CompareRow) Value(i int) string {
	if i < 0 || i >= len(r.Values) || r.Values[i] == "" {
		return "—"
	}
	return r.Values[i]
}

// Comparable reports whether every product states this spec.
func (r CompareRow) Comparable(products int) bool { return r.SharedBy >= products }

// Marked reports whether the row is shown as one the products disagree on: a
// spec only some of them state is already a quiet footnote, and is left as one.
func (r CompareRow) Marked(products int) bool { return r.Comparable(products) && r.Differs() }

// Differs reports whether the columns disagree about this spec. A product that
// does not state it counts as a value of its own: "has it" against "does not"
// is a difference worth showing.
func (r CompareRow) Differs() bool {
	for i := 1; i < len(r.Values); i++ {
		if r.Values[i] != r.Values[0] {
			return true
		}
	}
	return false
}

// CompareView is the comparison table.
type CompareView struct {
	Products []CompareProduct
	Rows     []CompareRow
	// Dropped is set when the link named more products than a comparison holds.
	Dropped bool
	// Query is the picker's search; Candidates are its results, without the
	// products already in the comparison.
	Query      string
	Candidates []CompareCandidate
	// Suggestions are what a comparison of one product could add: the other
	// products on its shelf, nearest in price first. Empty once there are two.
	Suggestions []ProductTile
	// ShelfSlug is the category the first product sits in.
	ShelfSlug string
	// StartSlug is where to begin choosing when nothing is chosen: the first
	// category that offers comparison, or "" when none does.
	StartSlug string
}

// StartHref is where to begin choosing products to compare: a category that
// offers the comparison, since a shelf without it has no box to tick.
func (v CompareView) StartHref() string {
	if v.StartSlug == "" {
		return "/"
	}
	return "/c/" + v.StartSlug
}

// ShelfHref is the shelf the chosen product sits on, where more to compare with
// can be found, or "" when the comparison names no product.
func (v CompareView) ShelfHref() string {
	if len(v.Products) == 0 || v.ShelfSlug == "" {
		return ""
	}
	return "/c/" + v.ShelfSlug
}

// Empty reports whether there is nothing to compare.
func (v CompareView) Empty() bool { return len(v.Products) == 0 }

// Enough reports whether there are enough columns to compare.
func (v CompareView) Enough() bool { return len(v.Products) >= MinCompare }

// Full reports whether the comparison has no room left.
func (v CompareView) Full() bool { return len(v.Products) >= MaxCompare }

// AddHref is the comparison with one more product. The set is the address, so
// adding is a link and writes nothing.
func (v CompareView) AddHref(slug string) string {
	var b strings.Builder
	b.WriteString("/compare")
	sep := "?"
	for i := range v.Products {
		b.WriteString(sep)
		b.WriteString("p=")
		b.WriteString(v.Products[i].Slug)
		sep = "&"
	}
	b.WriteString(sep)
	b.WriteString("p=")
	b.WriteString(slug)
	return b.String()
}

// Count is how many products are being compared.
func (v CompareView) Count() int { return len(v.Products) }

// ProductHref is one product's page, carrying the comparison it was reached from.
func (v CompareView) ProductHref(slug string) string {
	var b strings.Builder
	b.WriteString("/p/")
	b.WriteString(slug)
	sep := "?"
	for i := range v.Products {
		b.WriteString(sep)
		b.WriteString("p=")
		b.WriteString(v.Products[i].Slug)
		sep = "&"
	}
	return b.String()
}

// RemoveHref is the comparison without one product.
func (v CompareView) RemoveHref(slug string) string {
	var b strings.Builder
	b.WriteString("/compare")
	sep := "?"
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
