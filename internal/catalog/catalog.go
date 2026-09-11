// Package catalog renders goen's category listing and search pages.
//
// Two rules the SQL alone does not carry: a listing shows a category AND its
// descendants, and every variant-level filter must be satisfied by ONE variant.
package catalog

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNotFound is returned when a slug names no category.
var ErrNotFound = errors.New("catalog: not found")

// PageSize is how many products a listing page shows.
const PageSize = 24

// MaxQueryRunes bounds a search term.
const MaxQueryRunes = 100

// Sort is a listing's ordering. The zero value is the default, newest first.
type Sort string

// The orderings a listing offers.
const (
	SortNewest    Sort = ""
	SortPriceAsc  Sort = "price_asc"
	SortPriceDesc Sort = "price_desc"
	SortRating    Sort = "rating"
)

// ParseSort maps a query-string value to a Sort, falling back to the default.
func ParseSort(s string) Sort {
	switch Sort(s) {
	case SortPriceAsc:
		return SortPriceAsc
	case SortPriceDesc:
		return SortPriceDesc
	case SortRating:
		return SortRating
	default:
		return SortNewest
	}
}

// Filters is everything a listing URL can narrow by. MinPrice and MaxPrice are
// in minor units, and zero means "no bound".
type Filters struct {
	BrandSlugs  []string
	InStockOnly bool
	MinPrice    int64
	MaxPrice    int64
	Sort        Sort
	Page        int // 1-based; 0 and below are treated as 1
}

// FiltersVariants reports whether any filter has to be satisfied by a single
// variant. When none is set the listing skips the EXISTS entirely.
func (f Filters) FiltersVariants() bool {
	return f.InStockOnly || f.MinPrice > 0 || f.MaxPrice > 0
}

// Offset is the row offset for the requested page.
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

// Active reports whether anything narrows the listing, which decides whether
// the page offers a "clear all" control.
func (f Filters) Active() bool {
	return len(f.BrandSlugs) > 0 || f.FiltersVariants()
}

// maxPage bounds the page number so a URL cannot make the database count its
// way past the end of the catalogue.
const maxPage = 500

// ParsePage reads a 1-based page number, clamping anything invalid to 1 and
// anything absurd to maxPage.
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

// ParsePrice reads a price bound in whole New Taiwan dollars and returns minor
// units. Anything unparseable, negative or above the schema's cap is no bound.
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

// trimmedQuery is what both the ILIKE pattern and the echoed heading start
// from. Unicode space counts as space, so a query of only NBSP is empty for
// both rather than a searched term one side never queried.
func trimmedQuery(q string) string {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > MaxQueryRunes {
		return string(r[:MaxQueryRunes])
	}
	return q
}

// SearchPattern turns a visitor's words into an ILIKE pattern. Escaping happens
// before the wildcards are added, or it would escape goen's own.
func SearchPattern(q string) string {
	q = trimmedQuery(q)
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}
