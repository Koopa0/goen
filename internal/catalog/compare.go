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
const (
	MinCompare = 2
	MaxCompare = 4
)

// Compare reads the products a URL named and the specs that tell them apart. A
// slug naming no active product is dropped rather than refused.
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

	// Keyed on the untranslated label, which is the row's identity, and which is
	// what the query counts shared_by on. Two labels can share a translation.
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
