// Package catalog renders goen's category listing and search pages.
//
// Two rules the SQL alone does not carry: a listing shows a category AND its
// descendants, and every variant-level filter must be satisfied by ONE variant.
package catalog

import (
	"errors"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/web"
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

// Filters holds MinPrice and MaxPrice in minor units; zero means no bound.
type Filters struct {
	BrandSlugs  []string
	InStockOnly bool
	MinPrice    int64
	MaxPrice    int64
	Sort        Sort
	Page        int // 1-based; 0 and below are treated as 1
}

// VariantScoped is false when no filter needs a single variant, so the listing
// skips the EXISTS entirely.
func (f Filters) VariantScoped() bool {
	return f.InStockOnly || f.MinPrice > 0 || f.MaxPrice > 0
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
	return len(f.BrandSlugs) > 0 || f.VariantScoped()
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

// ParsePrice reads whole New Taiwan dollars and returns minor units; anything
// unparseable, negative or above the schema's cap is no bound.
func ParsePrice(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	const maxTWD = 100_000_000 // 10_000_000_000 minor units, the schema's cap
	if n > maxTWD {
		return 0
	}
	return n * 100
}

// Unicode space counts as space, so a query of only NBSP is empty for both the
// ILIKE pattern and the echoed heading rather than a term one side never
// queried.
func trimmedQuery(q string) string {
	q = strings.TrimSpace(web.FoldWidth(q))
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
		terms[i] = "%" + EscapeLike(t) + "%"
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

// likeEscaper covers the three characters LIKE and ILIKE read as syntax; the
// backslash is PostgreSQL's default escape character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike makes typed words match only themselves: without it a typed % or _
// is a wildcard, and "%%" matches every row.
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}
