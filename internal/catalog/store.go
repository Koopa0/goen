package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads the catalogue for the listing and search pages.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store reading through dbtx.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("catalog: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// Listing reads one page of a category listing, with its crumbs and facets. One
// set of descendant ids serves the listing, the count and the brand facet, which
// otherwise disagree about scope.
func (s *Store) Listing(ctx context.Context, slug string, f Filters) (pages.ListingView, error) {
	cat, err := s.q.CategoryBySlug(ctx, db.CategoryBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.ListingView{}, ErrNotFound
		}
		return pages.ListingView{}, fmt.Errorf("read category %q: %w", slug, err)
	}

	ids, err := s.q.CategoryDescendants(ctx, cat.ID)
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("read descendants of %q: %w", slug, err)
	}

	brands, err := s.q.CategoryBrands(ctx, ids)
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("read brands for %q: %w", slug, err)
	}

	brandIDs := make([]uuid.UUID, 0, len(f.BrandSlugs))
	selected := make(map[string]bool, len(f.BrandSlugs))
	for _, want := range f.BrandSlugs {
		selected[want] = true
	}
	for _, b := range brands {
		if selected[b.Slug] {
			brandIDs = append(brandIDs, b.ID)
		}
	}
	if len(f.BrandSlugs) > 0 && len(brandIDs) == 0 {
		// Every named brand was unknown here, and uuid.Nil matches no product —
		// an unfiltered page under a filtered URL would be the wrong answer.
		brandIDs = append(brandIDs, uuid.Nil)
	}

	rows, err := s.q.CategoryListing(ctx, db.CategoryListingParams{
		Locale:         string(i18n.FromContext(ctx)),
		CategoryIds:    ids,
		BrandIds:       brandIDs,
		FilterVariants: f.FiltersVariants(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
		Sort:           string(f.Sort),
		PageSize:       PageSize,
		PageOffset:     f.Offset(),
	})
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("read listing for %q: %w", slug, err)
	}

	total, err := s.q.CategoryListingCount(ctx, db.CategoryListingCountParams{
		CategoryIds:    ids,
		BrandIds:       brandIDs,
		FilterVariants: f.FiltersVariants(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
	})
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("count listing for %q: %w", slug, err)
	}

	view := pages.ListingView{
		Slug:     slug,
		Name:     cat.Name,
		Crumbs:   crumbs(cat.AncestorSlugs, cat.AncestorNames),
		Products: tiles(rows),
		Total:    total,
		Page:     max(f.Page, 1),
		PageSize: PageSize,
	}
	for _, b := range brands {
		view.Brands = append(view.Brands, pages.FacetOption{
			Value:    b.Slug,
			Label:    b.Name,
			Count:    b.ProductCount,
			Selected: selected[b.Slug],
		})
	}
	return view, nil
}

// Search reads one page of search results.
func (s *Store) Search(ctx context.Context, pattern string, page int) (pages.SearchView, error) {
	rows, err := s.q.SearchProducts(ctx, db.SearchProductsParams{
		Locale:     string(i18n.FromContext(ctx)),
		Pattern:    pattern,
		PageSize:   PageSize,
		PageOffset: offsetFor(page),
	})
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("search: %w", err)
	}
	total, err := s.q.SearchProductsCount(ctx, pattern)
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("count search: %w", err)
	}

	return pages.SearchView{
		Products: searchTiles(rows),
		Total:    total,
		Page:     max(page, 1),
		PageSize: PageSize,
	}, nil
}

// crumbs pairs the ancestor slugs with their names. The shorter array wins: a
// mismatched length would shift every label onto the wrong link.
func crumbs(slugs, names []string) []pages.Crumb {
	n := min(len(slugs), len(names))
	out := make([]pages.Crumb, 0, n)
	for i := range n {
		out = append(out, pages.Crumb{Slug: slugs[i], Name: names[i]})
	}
	return out
}

func tiles(rows []db.CategoryListingRow) []pages.ProductTile {
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
			Comparable:   true,
		})
	}
	return out
}

func searchTiles(rows []db.SearchProductsRow) []pages.ProductTile {
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
			Comparable:   true,
		})
	}
	return out
}

// Deals reads the products with something marked down.
func (s *Store) Deals(ctx context.Context, page int) (pages.SearchView, error) {
	rows, err := s.q.DealProducts(ctx, db.DealProductsParams{
		Locale:     string(i18n.FromContext(ctx)),
		PageSize:   PageSize,
		PageOffset: offsetFor(page),
	})
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("read deals: %w", err)
	}
	total, err := s.q.DealProductsCount(ctx)
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("count deals: %w", err)
	}
	return pages.SearchView{
		Products: dealTiles(rows),
		Total:    total,
		Page:     max(page, 1),
		PageSize: PageSize,
		Path:     "/deals",
	}, nil
}

// dealTiles is searchTiles for the deals query; sqlc emits a row struct per
// query, so one function cannot take both.
func dealTiles(rows []db.DealProductsRow) []pages.ProductTile {
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out
}

// SitemapProducts is every active product's slug and when it last changed.
func (s *Store) SitemapProducts(ctx context.Context, limit int32) ([]db.SitemapProductsRow, error) {
	rows, err := s.q.SitemapProducts(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("read sitemap products: %w", err)
	}
	return rows, nil
}

// SitemapCategories is every category that has something to sell.
func (s *Store) SitemapCategories(ctx context.Context) ([]db.SitemapCategoriesRow, error) {
	rows, err := s.q.SitemapCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("read sitemap categories: %w", err)
	}
	return rows, nil
}
