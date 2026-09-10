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
		return i18n.T(ctx, i18n.KeyCompareInStock)
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
