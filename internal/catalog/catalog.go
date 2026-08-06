// Package catalog renders goen's category listing and search pages.
//
// The read model is measured, not assumed — see
// docs/decisions/003-listing-read-model.md. Two rules from that measurement are
// carried here rather than left to the SQL alone, because a caller that gets
// them wrong produces a page that looks right:
//
//   - A listing shows a category AND its descendants.
//   - Every variant-level filter must be satisfied by ONE variant.
package catalog

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNotFound is returned when a slug names no category. The handler answers
// 404 with the site's own not-found page rather than an empty listing, because
// an empty listing tells a visitor the category exists and has nothing in it.
var ErrNotFound = errors.New("catalog: not found")

// PageSize is how many products a listing page shows. Four columns at the
// desktop artboard, so 24 fills six full rows and never leaves one product
// stranded on a row of its own at 2, 3 or 4 columns.
const PageSize = 24

// MaxQueryRunes bounds a search term. Long enough for any real product name;
// short enough that a pathological query cannot become a large ILIKE pattern.
const MaxQueryRunes = 100

// Sort is a listing's ordering. The zero value is the default, newest first.
//
// It is a closed set matched against a literal in SQL rather than text
// interpolated into an ORDER BY, which is where an injection would get in.
type Sort string

// The orderings a listing offers.
const (
	SortNewest    Sort = ""
	SortPriceAsc  Sort = "price_asc"
	SortPriceDesc Sort = "price_desc"
	SortRating    Sort = "rating"
)

// ParseSort maps a query-string value to a Sort, falling back to the default
// for anything unrecognised. An unknown sort is a visitor editing a URL, not an
// error worth a page: they get the default ordering.
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

// Filters is everything a listing URL can narrow by.
//
// MinPrice and MaxPrice are in minor units, and zero means "no bound" — TWD has
// no sub-dollar amounts, so a NT$0 bound would filter nothing anyway.
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

// Offset is the row offset for the requested page. ParsePage clamps Page to
// [1, maxPage], so the product cannot overflow int32 — but the conversion is
// written defensively rather than relying on a caller that set Page by hand.
func (f Filters) Offset() int32 {
	return offsetFor(f.Page)
}

// offsetFor turns a 1-based page into a row offset, clamped to the same bound
// ParsePage applies so an out-of-range Page set directly cannot produce a
// nonsense or overflowing offset.
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

// maxPage bounds the page number so a URL cannot ask for an offset far past the
// end of the catalogue and make the database count its way there.
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
// units. Anything unparseable or negative is no bound at all.
//
// The cap matches the schema's own ceiling on a variant price, so a bound above
// it is the same as no bound rather than a number the database cannot hold.
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

// SearchPattern turns a visitor's words into an ILIKE pattern.
//
// The escaping is the point. ILIKE reads %, _ and \ as syntax, so a search for
// "100%" without this finds every product rather than the one whose name
// contains a percent sign, and "a_b" matches "axb". Escaping happens before the
// wildcards are added, or it would escape goen's own.
//
// An empty or whitespace-only term yields "", which the handler treats as no
// search rather than a pattern matching the whole catalogue.
func SearchPattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	if r := []rune(q); len(r) > MaxQueryRunes {
		q = string(r[:MaxQueryRunes])
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}
