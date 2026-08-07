package catalog

import (
	"context"
	"fmt"
	"slices"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MinCompare and MaxCompare bound how many products a comparison holds.
//
// Two, because comparing one thing is a product page. Four, because a fifth
// column does not fit at 375 and the table becomes a horizontal scroll — which
// is the one interaction that makes a comparison harder than reading two pages
// separately.
const (
	MinCompare = 2
	MaxCompare = 4
)

// Compare reads the products a URL named and the specs that let them be told
// apart.
//
// A slug that is not an active product is dropped rather than refused: a
// comparison URL is shared and bookmarked, and one product being retired should
// not turn the whole link into an error page.
func (s *Store) Compare(ctx context.Context, slugs []string) (pages.CompareView, error) {
	slugs = normaliseSlugs(slugs)
	if len(slugs) == 0 {
		return pages.CompareView{}, nil
	}

	rows, err := s.q.CompareProducts(ctx, db.CompareProductsParams{
		Slugs: slugs, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CompareView{}, fmt.Errorf("read comparison: %w", err)
	}
	view := pages.CompareView{}
	at := make(map[string]int, len(rows))
	for i := range rows {
		r := &rows[i]
		at[r.Slug] = i
		view.Products = append(view.Products, pages.CompareProduct{
			Slug: r.Slug, Name: r.Name, Summary: r.Summary,
			Brand: r.Brand, Category: r.Category,
			PriceCents: r.MinPriceCents, CompareCents: r.CompareAtPriceCents.Int64,
			Rating: r.Rating, RatingCount: r.RatingCount, InStock: r.InStock,
			WarrantyMonths: int(r.WarrantyMonths),
			ImageURL:       assets.ProductImageURL(r.ImageKey),
			ImageSrcset:    assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:       r.ImageAlt,
		})
	}
	if len(view.Products) == 0 {
		return view, nil
	}

	specs, err := s.q.CompareSpecs(ctx, db.CompareSpecsParams{
		Slugs: slugs, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CompareView{}, fmt.Errorf("read comparison specs: %w", err)
	}

	// One row per label, with a cell per product. The query already ordered the
	// labels so the shared ones come first; this preserves that order and fills
	// the gaps, because a missing spec is a fact about the product and must
	// render as one rather than shifting the columns.
	// Keyed on the UNTRANSLATED label, which is the row's identity — never on the
	// localized text this renders. Two Chinese labels can translate to one
	// English word (輸出 and 孔位 are both "Ports" in the seed), and keying on
	// what the reader sees merged them into one row where the second product's
	// value overwrote the first: a spec present on the English PDP simply
	// vanished from the English comparison. The query counts shared_by on the
	// same untranslated label, so this is also what keeps the count and the
	// grouping talking about the same thing.
	rowAt := make(map[string]int)
	for i := range specs {
		sp := &specs[i]
		idx, seen := rowAt[sp.LabelKey]
		if !seen {
			idx = len(view.Rows)
			rowAt[sp.LabelKey] = idx
			view.Rows = append(view.Rows, pages.CompareRow{
				Label:    sp.Label,
				Values:   make([]string, len(view.Products)),
				SharedBy: int(sp.SharedBy),
			})
		}
		if col, ok := at[sp.Slug]; ok {
			view.Rows[idx].Values[col] = sp.Value
		}
	}
	return view, nil
}

// normaliseSlugs bounds and deduplicates what a URL asked for.
//
// Deduplicated because comparing a product with itself is a column of identical
// values that teaches nothing, and bounded because the slug list reaches a
// query — an unbounded one is an unbounded amount of work anybody can request.
func normaliseSlugs(raw []string) []string {
	out := make([]string, 0, MaxCompare)
	for _, s := range raw {
		if s == "" || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
		if len(out) == MaxCompare {
			break
		}
	}
	return out
}
