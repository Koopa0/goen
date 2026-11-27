// Package catalog renders goen's browsing pages: category listings, search,
// compare, and the product page with its reviews, questions and back-in-stock
// form (the request itself is backinstock's).
//
// Two rules the SQL alone does not carry: a listing shows a category AND its
// descendants, and every variant-level filter must be satisfied by ONE variant.
package catalog

import (
	"errors"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/web/formtext"
)

var ErrNotFound = errors.New("catalog: not found")

const PageSize = 24

const MaxQueryRunes = 100

// Sort orders a listing or search: a price or rating sort leads and the page's own order breaks its ties:
// newest first on a listing, best match on a search.
type Sort string

// SortNewest and SortRelevance are what a page shows when the shopper chose
// none, so neither appears in an address.
const (
	SortNewest    Sort = "newest"
	SortRelevance Sort = "relevance"
	SortPriceAsc  Sort = "price_asc"
	SortPriceDesc Sort = "price_desc"
	SortRating    Sort = "rating"
)

func ParseSort(s string, unchosen Sort) Sort {
	switch Sort(s) {
	case SortPriceAsc:
		return SortPriceAsc
	case SortPriceDesc:
		return SortPriceDesc
	case SortRating:
		return SortRating
	default:
		return unchosen
	}
}

func (s Sort) Param() string {
	if s == SortNewest || s == SortRelevance {
		return ""
	}
	return string(s)
}

// Filters is what the shopper narrowed a shelf to, each value already bounded.
// Department is a search's: the slug of the department its matches are kept to.
type Filters struct {
	OptionValues []OptionFilter
	BrandSlugs   []string
	InStockOnly  bool
	// OnSaleOnly keeps what a card would strike: campaign.OnSale's question,
	// asked of the variant the card prices.
	OnSaleOnly bool
	Price      PriceBand
	Department string
	Sort       Sort
	Page       int // 1-based; 0 and below are treated as 1
}

// VariantScoped is false when no filter needs a single variant, so the listing
// skips the EXISTS entirely.
func (f Filters) VariantScoped() bool {
	return len(f.OptionValues) > 0 || f.InStockOnly || f.OnSaleOnly || f.Price.Bounded()
}

func (f Filters) Offset() int32 {
	return offsetFor(f.Page)
}

func offsetFor(page int) int32 {
	if page <= 1 {
		return 0
	}
	if page > maxPage {
		page = maxPage
	}
	return int32((page - 1) * PageSize)
}

func (f Filters) Active() bool {
	return len(f.BrandSlugs) > 0 || f.Department != "" || f.VariantScoped()
}

// maxPage stops a URL from making the database count its way past the end of
// the catalogue.
const maxPage = 500

func ParsePage(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > maxPage {
		return maxPage
	}
	return n
}

// PriceBand is one of the price ranges a shelf offers, [Min, Max] in minor
// units, 0 for no bound. The zero band is any price. ID is the band's value in
// an address.
type PriceBand struct {
	ID       string
	Min, Max int64
}

// priceBands are the ranges every shelf offers, cheapest first. Each ends a
// cent below the next one's start, so no price falls between two or in both.
var priceBands = []PriceBand{
	{ID: "under-1000", Max: 99_999},
	{ID: "1000-9999", Min: 100_000, Max: 999_999},
	{ID: "10000-up", Min: 1_000_000},
}

// parsePriceBand is the band an address names, or any price for a name it does
// not know.
func parsePriceBand(id string) PriceBand {
	for _, b := range priceBands {
		if b.ID == id {
			return b
		}
	}
	return PriceBand{}
}

// Bounded is whether the band limits the price at all.
func (b PriceBand) Bounded() bool { return b.Min > 0 || b.Max > 0 }

// Unicode space counts as space, so a query of only NBSP is empty for both the
// ILIKE pattern and the echoed heading rather than a term one side never
// queried.
func trimmedQuery(q string) string {
	q = strings.TrimSpace(formtext.FoldWidth(q))
	if r := []rune(q); len(r) > MaxQueryRunes {
		return string(r[:MaxQueryRunes])
	}
	return q
}

// MaxSearchTerms bounds the terms because each adds a predicate to a query that
// scans every product.
const MaxSearchTerms = 5

// SearchPattern joins one pattern per term by a space; a term holds no
// whitespace, so SearchTerms can split it again. Escaping happens before the
// wildcards are added, or it would escape goen's own.
func SearchPattern(q string) string {
	terms := strings.Fields(trimmedQuery(q))
	if len(terms) > MaxSearchTerms {
		terms = terms[:MaxSearchTerms]
	}
	for i, t := range terms {
		terms[i] = "%" + db.EscapeLike(t) + "%"
	}
	return strings.Join(terms, " ")
}

func SearchTerms(pattern string) (terms []string, exact string) {
	terms = strings.Fields(pattern)
	bare := make([]string, len(terms))
	for i, t := range terms {
		bare[i] = strings.TrimSuffix(strings.TrimPrefix(t, "%"), "%")
	}
	return terms, strings.Join(bare, " ")
}
