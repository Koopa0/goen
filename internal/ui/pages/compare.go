package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// MinCompare and MaxCompare bound a comparison. The comparison is the URL, so
// these are the only place either number is written.
const (
	MinCompare = 2
	MaxCompare = 4
)

type CompareCandidate struct {
	Slug  string
	Name  string
	Brand string
}

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

func (p CompareProduct) Price() string { return twd(p.PriceCents) }

func (p CompareProduct) Stock(ctx context.Context) string {
	if p.InStock {
		return i18n.T(ctx, i18n.KeyInStock)
	}
	return i18n.T(ctx, i18n.KeySoldOut)
}

func (p CompareProduct) RatingText() string {
	if p.RatingCount == 0 {
		return "—"
	}
	return strconv.FormatFloat(p.Rating, 'f', 1, 64) +
		" (" + strconv.FormatInt(p.RatingCount, 10) + ")"
}

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

type CompareRow struct {
	Label    string
	Values   []string
	SharedBy int
}

func (r CompareRow) Value(i int) string {
	if i < 0 || i >= len(r.Values) || r.Values[i] == "" {
		return "—"
	}
	return r.Values[i]
}

func (r CompareRow) Comparable(products int) bool { return r.SharedBy >= products }

// Marked is false for a spec only some products state: it is already a quiet footnote.
func (r CompareRow) Marked(products int) bool { return r.Comparable(products) && r.Differs() }

// Differs counts a product that does not state a spec as a value of its own: "has it"
// against "does not" is a difference worth showing.
func (r CompareRow) Differs() bool {
	for i := 1; i < len(r.Values); i++ {
		if r.Values[i] != r.Values[0] {
			return true
		}
	}
	return false
}

type CompareView struct {
	Products []CompareProduct
	Rows     []CompareRow
	// Dropped is set when the link named more products than a comparison holds.
	Dropped    bool
	Query      string
	Candidates []CompareCandidate
	// Suggestions are empty once there are two products.
	Suggestions []ProductTile
	ShelfSlug   string
	// StartSlug is "" when no category offers comparison.
	StartSlug string
}

// StartHref is a category that offers comparison: a shelf without it has no box to tick.
func (v CompareView) StartHref() string {
	if v.StartSlug == "" {
		return "/"
	}
	return "/c/" + v.StartSlug
}

func (v CompareView) ShelfHref() string {
	if len(v.Products) == 0 || v.ShelfSlug == "" {
		return ""
	}
	return "/c/" + v.ShelfSlug
}

func (v CompareView) Empty() bool { return len(v.Products) == 0 }

func (v CompareView) Enough() bool { return len(v.Products) >= MinCompare }

func (v CompareView) Full() bool { return len(v.Products) >= MaxCompare }

// AddHref is a link, not a write: the set is the address.
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

func (v CompareView) Count() int { return len(v.Products) }

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
