package catalog

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
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
	PriceVaries    bool
	CompareCents   int64
	OnSale         bool
	Rating         float64
	RatingCount    int64
	InStock        bool
	WarrantyMonths int
	// SoleVariantID is set only when the product has one variant and it is in
	// stock: the only case where an add-to-cart in the column names what goes in
	// the cart.
	SoleVariantID string
	ImageURL      string
	ImageSrcset   string
	ImageAlt      string
}

func (p *CompareProduct) PriceTile() tile.Tile {
	return tile.Tile{
		PriceCents: p.PriceCents, CompareCents: p.CompareCents, OnSale: p.OnSale,
		PriceVaries: p.PriceVaries, InStock: p.InStock,
	}
}

func (p *CompareProduct) RatingText() string {
	if p.RatingCount == 0 {
		return "—"
	}
	return strconv.FormatFloat(p.Rating, 'f', 1, 64) +
		" (" + strconv.FormatInt(p.RatingCount, 10) + ")"
}

func (p *CompareProduct) Warranty(ctx context.Context) string {
	if p.WarrantyMonths == 0 {
		return "—"
	}
	return i18n.Count(ctx, i18n.KeyUnitMonths, int64(p.WarrantyMonths), int64(p.WarrantyMonths))
}

type CompareRow struct {
	Label    string
	Values   []string
	SharedBy int
}

func (r CompareRow) Value(i int) string {
	if !r.Stated(i) {
		return "—"
	}
	return r.Values[i]
}

func (r CompareRow) Stated(i int) bool { return i >= 0 && i < len(r.Values) && r.Values[i] != "" }

func (r CompareRow) Comparable(products int) bool { return r.SharedBy >= products }

// Marked is false for a spec only some products state: "has it" against "does not" is not a comparison of values.
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
	// Kept says the products are the shopper's compare set, read from its
	// cookie, rather than a link's ?p=: adding and removing then post to the set
	// instead of linking to another address.
	Kept bool
	// Presets are the ready-made comparisons, offered whatever is shown.
	Presets []ComparePreset
	// Dropped is set when the link named more products than a comparison holds.
	Dropped    bool
	Query      string
	Candidates []CompareCandidate
	// Suggestions are empty once there are two products.
	Suggestions []tile.Tile
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

// Showing says this comparison holds exactly the preset's products, in whatever
// order, so the preset is marked as the page the shopper is on. A kept set is
// never a preset's link.
func (v CompareView) Showing(p ComparePreset) bool {
	if v.Kept || len(v.Products) != len(p.Slugs) {
		return false
	}
	for i := range v.Products {
		if !slices.Contains(p.Slugs, v.Products[i].Slug) {
			return false
		}
	}
	return true
}

// Facts are the rows a comparison has before the specifications: the rating and
// the warranty where any product states one, and the department only when the
// products are not all from one. A product with no reviews or no stated
// warranty states nothing, so its cell is the dash an unstated specification
// gets; a row of nothing but dashes is left out.
func (v CompareView) Facts(ctx context.Context) []CompareRow {
	n := len(v.Products)
	rating := CompareRow{Label: i18n.T(ctx, i18n.KeyCompareRowRating), Values: make([]string, n)}
	warranty := CompareRow{Label: i18n.T(ctx, i18n.KeyCompareRowWarranty), Values: make([]string, n)}
	category := CompareRow{Label: i18n.T(ctx, i18n.KeyCompareRowCategory), Values: make([]string, n), SharedBy: n}
	for i := range v.Products {
		p := &v.Products[i]
		if p.RatingCount > 0 {
			rating.Values[i] = p.RatingText()
			rating.SharedBy++
		}
		if p.WarrantyMonths > 0 {
			warranty.Values[i] = p.Warranty(ctx)
			warranty.SharedBy++
		}
		category.Values[i] = p.Category
	}
	var facts []CompareRow
	for _, row := range []CompareRow{rating, warranty} {
		if row.SharedBy > 0 {
			facts = append(facts, row)
		}
	}
	if category.Differs() {
		facts = append(facts, category)
	}
	return facts
}

// Filterable says the differences-only switch would change something: some row
// differs and some does not. Otherwise it is not offered.
func (v CompareView) Filterable(ctx context.Context) bool {
	rows := append(v.Facts(ctx), v.Rows...)
	return slices.ContainsFunc(rows, CompareRow.Differs) &&
		slices.ContainsFunc(rows, func(r CompareRow) bool { return !r.Differs() })
}

// ComparePreset is a ready-made comparison: the newest products in stock of one
// category that offers comparison. It is a link, as any comparison by ?p= is.
type ComparePreset struct {
	Category string
	Slugs    []string
	// slug is the category's: two categories may share a name.
	slug string
}

func (p ComparePreset) Href() string {
	return "/compare?" + url.Values{"p": p.Slugs}.Encode()
}

func (p ComparePreset) Label(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyComparePreset), len(p.Slugs), p.Category)
}
