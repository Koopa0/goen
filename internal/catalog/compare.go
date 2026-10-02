package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Compare drops a slug naming no active product rather than refusing it.
func (s *Store) Compare(ctx context.Context, slugs []string) (pages.CompareView, error) {
	slugs, dropped := normaliseSlugs(slugs)
	if len(slugs) == 0 {
		return s.withStart(ctx, pages.CompareView{})
	}

	rows, err := s.q.CompareProducts(ctx, db.CompareProductsParams{
		Slugs: slugs, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CompareView{}, fmt.Errorf("read comparison: %w", err)
	}
	view := pages.CompareView{Dropped: dropped}
	if len(rows) > 0 {
		view.ShelfSlug = rows[0].CategorySlug
	}
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
		return s.withStart(ctx, view)
	}
	if len(view.Products) < pages.MinCompare {
		if view.Suggestions, err = s.compareSuggestions(ctx, &view.Products[0], slugs); err != nil {
			return pages.CompareView{}, err
		}
		return view, nil
	}

	specs, err := s.q.CompareSpecs(ctx, db.CompareSpecsParams{
		Slugs: slugs, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CompareView{}, fmt.Errorf("read comparison specs: %w", err)
	}

	// Keyed on the untranslated label, which is the row's identity and what the
	// query counts shared_by on; two labels can share a translation.
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

func normaliseSlugs(raw []string) (out []string, dropped bool) {
	out = make([]string, 0, pages.MaxCompare)
	for _, s := range raw {
		if s == "" || slices.Contains(out, s) {
			continue
		}
		if len(out) == pages.MaxCompare {
			return out, true
		}
		out = append(out, s)
	}
	return out, false
}

const suggestionCount = 6

func (s *Store) withStart(ctx context.Context, view pages.CompareView) (pages.CompareView, error) {
	slug, err := s.q.FirstComparableCategorySlug(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pages.CompareView{}, fmt.Errorf("read comparable category: %w", err)
	}
	view.StartSlug = slug
	return view, nil
}

func (s *Store) compareSuggestions(ctx context.Context, anchor *pages.CompareProduct, chosen []string) ([]pages.ProductTile, error) {
	rows, err := s.q.CompareSuggestions(ctx, db.CompareSuggestionsParams{
		Locale:       string(i18n.FromContext(ctx)),
		ProductSlug:  anchor.Slug,
		ExcludeSlugs: chosen,
		AnchorCents:  anchor.PriceCents,
		RowLimit:     suggestionCount,
	})
	if err != nil {
		return nil, fmt.Errorf("read comparison suggestions: %w", err)
	}
	return suggestionTiles(rows), nil
}

func suggestionTiles(rows []db.CompareSuggestionsRow) []pages.ProductTile {
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			PriceCents: r.MinPriceCents, PriceVaries: r.PriceVaries,
			ImageURL:    assets.ProductImageURL(r.ImageKey),
			ImageSrcset: assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:    r.ImageAlt, ImageWidth: r.ImageWidth, ImageHeight: r.ImageHeight,
		})
	}
	return out
}
