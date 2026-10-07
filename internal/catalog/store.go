package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

type Store struct {
	q        *db.Queries
	now      func() time.Time
	noPickup bool
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("catalog: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx), now: time.Now}
}

// WithoutPickup is for a deployment whose store map is not configured: checkout
// offers no pickup there, so what this store describes must not either.
func (s *Store) WithoutPickup() *Store {
	c := *s
	c.noPickup = true
	return &c
}

func (s *Store) ShopRules(ctx context.Context) (pages.ShopRules, error) {
	return ShopRules(ctx, s.q, !s.noPickup)
}

// ShopRules reads the rules the shop states about itself. withPickup is false where
// checkout offers no pickup, so a threshold or floor must not count it.
func ShopRules(ctx context.Context, q *db.Queries, withPickup bool) (pages.ShopRules, error) {
	freeOver, err := q.FreeDeliveryThreshold(ctx, withPickup)
	if err != nil {
		return pages.ShopRules{}, fmt.Errorf("read free delivery threshold: %w", err)
	}
	lowestFee, err := q.LowestDeliveryFee(ctx, withPickup)
	if err != nil {
		return pages.ShopRules{}, fmt.Errorf("read lowest delivery fee: %w", err)
	}
	return pages.ShopRules{FreeDeliveryCents: freeOver, LowestFeeCents: lowestFee, PickupOffered: withPickup}, nil
}

// Listing uses one set of descendant ids for the listing, the count and the
// brand facet, which otherwise disagree about scope.
func (s *Store) Listing(ctx context.Context, slug string, f Filters) (pages.ListingView, error) {
	view, _, err := s.listing(ctx, slug, f)
	return view, err
}

// ListingPage is Listing with the shopper's filters on the view and, when withHead is set, the
// department head, which reuses the comparable categories the listing has just read.
func (s *Store) ListingPage(ctx context.Context, slug string, f Filters, withHead bool) (pages.ListingView, *pages.DepartmentHead, error) {
	view, offers, err := s.listing(ctx, slug, f)
	if err != nil {
		return pages.ListingView{}, nil, err
	}
	view.Query = canonicalQuery(f)
	view.Filtered = f.Active()
	view.InStockOnly = f.InStockOnly
	view.MinPrice = f.MinPrice
	view.MaxPrice = f.MaxPrice
	view.Sort = f.Sort.Param()
	if !withHead {
		return view, nil, nil
	}
	head, err := s.DepartmentHead(ctx, slug, view.Theme.Comparable, view.IsFront(), offers)
	return view, head, err
}

func (s *Store) listing(ctx context.Context, slug string, f Filters) (pages.ListingView, map[uuid.UUID]bool, error) {
	cat, err := s.q.CategoryBySlug(ctx, db.CategoryBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.ListingView{}, nil, ErrNotFound
		}
		return pages.ListingView{}, nil, fmt.Errorf("read category %q: %w", slug, err)
	}

	ids, err := s.q.CategoryDescendants(ctx, cat.ID)
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("read descendants of %q: %w", slug, err)
	}

	names, values := optionFilterColumns(f.OptionValues)
	brands, err := s.q.CategoryBrands(ctx, db.CategoryBrandsParams{
		OptionNames: names, OptionValues: values,
		CategoryIds:    ids,
		FilterVariants: f.VariantScoped(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
	})
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("read brands for %q: %w", slug, err)
	}

	brandIDs, selected := selectedListingBrands(brands, f.BrandSlugs)

	rows, err := s.q.CategoryListing(ctx, db.CategoryListingParams{
		OptionNames: names, OptionValues: values,
		Locale:         string(i18n.FromContext(ctx)),
		CategoryIds:    ids,
		BrandIds:       brandIDs,
		FilterVariants: f.VariantScoped(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
		Sort:           f.Sort.Param(),
		PageSize:       PageSize,
		PageOffset:     f.Offset(),
	})
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("read listing for %q: %w", slug, err)
	}

	total, err := s.q.CategoryListingCount(ctx, db.CategoryListingCountParams{
		OptionNames: names, OptionValues: values,
		CategoryIds:    ids,
		BrandIds:       brandIDs,
		FilterVariants: f.VariantScoped(),
		InStockOnly:    f.InStockOnly,
		MinPrice:       f.MinPrice,
		MaxPrice:       f.MaxPrice,
	})
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("count listing for %q: %w", slug, err)
	}

	trail := crumbs(cat.AncestorSlugs, cat.AncestorNames)
	department := slug
	if len(trail) > 0 {
		department = trail[0].Slug
	}
	children, err := s.q.CategoryChildren(ctx, db.CategoryChildrenParams{
		Slug: department, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("read children of %q: %w", department, err)
	}

	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return pages.ListingView{}, nil, err
	}

	view := pages.ListingView{
		Slug:   slug,
		Name:   cat.Name,
		Crumbs: trail,
		Theme: &pages.Theme{
			Children:   childCrumbs(children),
			Tone:       pages.ResolveTone(cat.Tone),
			Comparable: offers[cat.ID],
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

	err = s.withHighlights(ctx, view.Products)
	if err != nil {
		return pages.ListingView{}, nil, err
	}

	if len(brands) > 0 {
		view.Facets = append(view.Facets, pages.FacetGroup{Kind: pages.FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: brandFacets(brands, selected)})
	}
	optionRows, err := s.q.CategoryOptionValues(ctx, db.CategoryOptionValuesParams{
		Locale: string(i18n.FromContext(ctx)), CategoryIds: ids, BrandIds: brandIDs,
		InStockOnly: f.InStockOnly, MinPrice: f.MinPrice, MaxPrice: f.MaxPrice,
		OptionNames: names, OptionValues: values,
	})
	if err != nil {
		return pages.ListingView{}, nil, fmt.Errorf("read option facets for %q: %w", slug, err)
	}
	view.Facets = append(view.Facets, optionFacets(optionRows, f.OptionValues)...)
	return view, offers, nil
}

func (s *Store) Search(ctx context.Context, pattern string, sort Sort, page int) (pages.SearchView, error) {
	terms, exact := SearchTerms(pattern)
	rows, err := s.q.SearchProducts(ctx, db.SearchProductsParams{
		Locale:       string(i18n.FromContext(ctx)),
		Patterns:     terms,
		ExactPattern: exact,
		Sort:         sort.Param(),
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
		Sort:     sort.Param(),
	}, nil
}

func (s *Store) NewestProducts(ctx context.Context, n int32) ([]pages.ProductTile, error) {
	rows, err := s.q.NewestProducts(ctx, db.NewestProductsParams{
		Locale:   string(i18n.FromContext(ctx)),
		PageSize: n,
	})
	if err != nil {
		return nil, fmt.Errorf("read newest products: %w", err)
	}
	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return nil, err
	}
	// Same columns in the same order as SearchProducts, so one tile builder
	// serves both.
	asSearch := make([]db.SearchProductsRow, len(rows))
	for i := range rows {
		asSearch[i] = db.SearchProductsRow(rows[i])
	}
	return searchTiles(asSearch, offers), nil
}

func childCrumbs(rows []db.CategoryChildrenRow) []pages.Crumb {
	out := make([]pages.Crumb, 0, len(rows))
	for _, r := range rows {
		out = append(out, pages.Crumb{Slug: r.Slug, Name: r.Name})
	}
	return out
}

// The shorter array wins: a mismatched length would shift every label onto the
// wrong link.
func crumbs(slugs, names []string) []pages.Crumb {
	n := min(len(slugs), len(names))
	out := make([]pages.Crumb, 0, n)
	for i := range n {
		out = append(out, pages.Crumb{Slug: slugs[i], Name: names[i]})
	}
	return out
}

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

// comparableCategories is shared by listing and search so the two cannot
// disagree about a product.
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
			InCampaign:   r.InCampaign,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
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
			InCampaign:   r.InCampaign,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
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

// dealTiles exists because sqlc emits a row struct per query, so one function
// cannot take both.
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
			InCampaign:   r.InCampaign,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out
}

func (s *Store) SitemapProducts(ctx context.Context, limit int32) ([]db.SitemapProductsRow, error) {
	rows, err := s.q.SitemapProducts(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("read sitemap products: %w", err)
	}
	return rows, nil
}

func (s *Store) SitemapCategories(ctx context.Context, limit int32) ([]db.SitemapCategoriesRow, error) {
	rows, err := s.q.SitemapCategories(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("read sitemap categories: %w", err)
	}
	return rows, nil
}

func optionFacets(rows []db.CategoryOptionValuesRow, selected []OptionFilter) []pages.FacetGroup {
	groups := make([]pages.FacetGroup, 0)
	positions := make(map[string]int)
	chosen := make(map[OptionFilter]bool, len(selected))
	for _, pair := range selected {
		chosen[pair] = true
	}
	for i := range rows {
		row := &rows[i]
		position, found := positions[row.OptionName]
		if !found {
			position = len(groups)
			positions[row.OptionName] = position
			groups = append(groups, pages.FacetGroup{Kind: pages.FacetVariantOption, Name: row.OptionName, Label: row.OptionLabel})
		}
		groups[position].Options = append(groups[position].Options, pages.FacetOption{
			Value: row.OptionName + ":" + row.Value, Label: row.ValueLabel, Count: row.ProductCount,
			Selected: chosen[OptionFilter{Name: row.OptionName, Value: row.Value}],
		})
	}
	return groups
}

func selectedListingBrands(brands []db.CategoryBrandsRow, slugs []string) (brandIDs []uuid.UUID, selected map[string]bool) {
	brandIDs = make([]uuid.UUID, 0, len(slugs))
	selected = make(map[string]bool, len(slugs))
	for _, want := range slugs {
		selected[want] = true
	}
	for _, b := range brands {
		if selected[b.Slug] {
			brandIDs = append(brandIDs, b.ID)
		}
	}
	if len(slugs) > 0 && len(brandIDs) == 0 {
		// Every named brand was unknown here, and uuid.Nil matches no product:
		// an unfiltered page under a filtered URL would be the wrong answer.
		brandIDs = append(brandIDs, uuid.Nil)
	}

	return brandIDs, selected
}
