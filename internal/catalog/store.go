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
		FilterVariants: f.VariantScoped(),
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
		FilterVariants: f.VariantScoped(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
	})
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("count listing for %q: %w", slug, err)
	}

	trail := crumbs(cat.AncestorSlugs, cat.AncestorNames)
	// The chips are the department's children from any page under it, so a
	// shopper in one sub-category sees the others.
	department := slug
	if len(trail) > 0 {
		department = trail[0].Slug
	}
	children, err := s.q.CategoryChildren(ctx, db.CategoryChildrenParams{
		Slug: department, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.ListingView{}, fmt.Errorf("read children of %q: %w", department, err)
	}

	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return pages.ListingView{}, err
	}

	view := pages.ListingView{
		Slug:   slug,
		Name:   cat.Name,
		Crumbs: trail,
		Theme: &pages.Theme{
			Children: childCrumbs(children),
			Tone:     pages.ResolveTone(cat.Tone),
			Photo: pages.Photo{
				URL:    assets.ProductImageURL(cat.ImageKey),
				Srcset: assets.ProductImageSrcsetAt(cat.ImageKey, int(cat.ImageWidth)),
				Alt:    cat.ImageAlt,
			},
		},
		Products: tiles(rows, offers),
		Total:    total,
		Page:     int32(min(max(f.Page, 1), maxPage)),
		PageSize: PageSize,
	}
	view.Brands = brandFacets(brands, selected)
	return view, nil
}

// Search reads one page of search results. The pattern is SearchPattern's, and
// sort is ParseSort's: on a search the default is best match, not newest.
func (s *Store) Search(ctx context.Context, pattern string, sort Sort, page int) (pages.SearchView, error) {
	terms, exact := SearchTerms(pattern)
	rows, err := s.q.SearchProducts(ctx, db.SearchProductsParams{
		Locale:       string(i18n.FromContext(ctx)),
		Patterns:     terms,
		ExactPattern: exact,
		Sort:         string(sort),
		PageSize:     PageSize,
		PageOffset:   offsetFor(page),
	})
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("search: %w", err)
	}
	total, err := s.q.SearchProductsCount(ctx, terms)
	if err != nil {
		return pages.SearchView{}, fmt.Errorf("count search: %w", err)
	}
	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return pages.SearchView{}, err
	}

	return pages.SearchView{
		Products: searchTiles(rows, offers),
		Total:    total,
		Page:     max(page, 1),
		PageSize: PageSize,
		Sort:     string(sort),
	}, nil
}

// NewestProducts reads through SearchProducts with no terms, which matches every
// active product and leaves only the newest-first tiebreak.
func (s *Store) NewestProducts(ctx context.Context, n int32) ([]pages.ProductTile, error) {
	rows, err := s.q.SearchProducts(ctx, db.SearchProductsParams{
		Locale:   string(i18n.FromContext(ctx)),
		Patterns: []string{},
		PageSize: n,
	})
	if err != nil {
		return nil, fmt.Errorf("read newest products: %w", err)
	}
	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return nil, err
	}
	return searchTiles(rows, offers), nil
}

func childCrumbs(rows []db.CategoryChildrenRow) []pages.Crumb {
	out := make([]pages.Crumb, 0, len(rows))
	for _, r := range rows {
		out = append(out, pages.Crumb{Slug: r.Slug, Name: r.Name})
	}
	return out
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

// brandFacets is the brand filter's options, the chosen ones marked.
func brandFacets(brands []db.CategoryBrandsRow, selected map[string]bool) []pages.FacetOption {
	out := make([]pages.FacetOption, 0, len(brands))
	for _, b := range brands {
		out = append(out, pages.FacetOption{
			Value:    b.Slug,
			Label:    b.Name,
			Count:    b.ProductCount,
			Selected: selected[b.Slug],
		})
	}
	return out
}

// comparableCategories is the set of categories that offer comparison, which a
// listing and a search both read, so the two cannot disagree about a product.
func (s *Store) comparableCategories(ctx context.Context) (map[uuid.UUID]bool, error) {
	ids, err := s.q.ComparableCategoryIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("read comparable categories: %w", err)
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func tiles(rows []db.CategoryListingRow, offers map[uuid.UUID]bool) []pages.ProductTile {
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
			Comparable:   offers[r.CategoryID],
		})
	}
	return out
}

func searchTiles(rows []db.SearchProductsRow, offers map[uuid.UUID]bool) []pages.ProductTile {
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
			Comparable:   offers[r.CategoryID],
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
			PriceCents:   r.TilePriceCents,
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

// SitemapCategories is the newest categories that have something to sell,
// bounded by the sitemap document's remaining capacity.
func (s *Store) SitemapCategories(ctx context.Context, limit int32) ([]db.SitemapCategoriesRow, error) {
	rows, err := s.q.SitemapCategories(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("read sitemap categories: %w", err)
	}
	return rows, nil
}
