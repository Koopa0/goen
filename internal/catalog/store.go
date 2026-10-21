package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/backinstock"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/tone"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/tile"
)

type Store struct {
	dbtx db.DBTX
	q    *db.Queries
	// policy reads the shop rules the department and product pages show.
	policy *policy.Store
	// backInStock records the requests the product page's form posts.
	backInStock *backinstock.Store
	log         *slog.Logger
	now         func() time.Time
}

func NewStore(dbtx db.DBTX, log *slog.Logger) *Store {
	if dbtx == nil || log == nil {
		panic("catalog: NewStore requires a database handle and a logger")
	}
	return &Store{
		dbtx: dbtx, q: db.New(dbtx), policy: policy.NewStore(dbtx), backInStock: backinstock.NewStore(dbtx),
		log: log, now: time.Now,
	}
}

// WithoutPickup is for a deployment whose store map is not configured: checkout
// offers no pickup there, so what this store describes must not either.
func (s *Store) WithoutPickup() *Store {
	c := *s
	c.policy = s.policy.WithoutPickup()
	return &c
}

func (s *Store) ShopRules(ctx context.Context) (policy.Rules, error) {
	return s.policy.Rules(ctx)
}

// Listing uses one set of descendant ids for the listing, the count and the
// brand facet, which otherwise disagree about scope.
func (s *Store) Listing(ctx context.Context, slug string, f Filters) (ListingView, error) {
	view, _, err := s.listing(ctx, slug, f)
	return view, err
}

// ListingPage is Listing with the shopper's filters on the view and, when withHead is set, the
// department head, which reuses the comparable categories the listing has just read.
func (s *Store) ListingPage(ctx context.Context, slug string, f Filters, withHead bool) (ListingView, *DepartmentHead, error) {
	view, offers, err := s.listing(ctx, slug, f)
	if err != nil {
		return ListingView{}, nil, err
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

func (s *Store) listing(ctx context.Context, slug string, f Filters) (ListingView, map[uuid.UUID]bool, error) {
	cat, err := s.q.CategoryBySlug(ctx, db.CategoryBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ListingView{}, nil, ErrNotFound
		}
		return ListingView{}, nil, fmt.Errorf("read category %q: %w", slug, err)
	}

	ids, err := s.q.CategoryDescendants(ctx, cat.ID)
	if err != nil {
		return ListingView{}, nil, fmt.Errorf("read descendants of %q: %w", slug, err)
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
		return ListingView{}, nil, fmt.Errorf("read brands for %q: %w", slug, err)
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
		return ListingView{}, nil, fmt.Errorf("read listing for %q: %w", slug, err)
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
		return ListingView{}, nil, fmt.Errorf("count listing for %q: %w", slug, err)
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
		return ListingView{}, nil, fmt.Errorf("read children of %q: %w", department, err)
	}

	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return ListingView{}, nil, err
	}

	photo := assets.Image{Digest: cat.ImageDigest, BundledKey: cat.ImageBundledKey, Width: int(cat.ImageWidth)}
	view := ListingView{
		Slug:   slug,
		Name:   cat.Name,
		Crumbs: trail,
		Theme: &Theme{
			Children:   childCrumbs(children),
			Tone:       tone.Resolve(cat.Tone),
			Comparable: offers[cat.ID],
			Photo: components.Photo{
				URL:    photo.URL(),
				Srcset: photo.Srcset(),
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
		return ListingView{}, nil, err
	}

	if len(brands) > 0 {
		view.Facets = append(view.Facets, FacetGroup{Kind: FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: brandFacets(brands, selected)})
	}
	optionRows, err := s.q.CategoryOptionValues(ctx, db.CategoryOptionValuesParams{
		Locale: string(i18n.FromContext(ctx)), CategoryIds: ids, BrandIds: brandIDs,
		InStockOnly: f.InStockOnly, MinPrice: f.MinPrice, MaxPrice: f.MaxPrice,
		OptionNames: names, OptionValues: values,
	})
	if err != nil {
		return ListingView{}, nil, fmt.Errorf("read option facets for %q: %w", slug, err)
	}
	view.Facets = append(view.Facets, optionFacets(optionRows, f.OptionValues)...)
	return view, offers, nil
}

func (s *Store) Search(ctx context.Context, pattern string, sort Sort, page int) (SearchView, error) {
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
		return SearchView{}, fmt.Errorf("search: %w", err)
	}
	total, err := s.q.SearchProductsCount(ctx, terms)
	if err != nil {
		return SearchView{}, fmt.Errorf("count search: %w", err)
	}
	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return SearchView{}, err
	}

	return SearchView{
		Products: searchTiles(rows, offers),
		Total:    total,
		Page:     max(page, 1),
		PageSize: PageSize,
		Sort:     sort.Param(),
	}, nil
}

func (s *Store) NewestProducts(ctx context.Context, n int32) ([]tile.Tile, error) {
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

func childCrumbs(rows []db.CategoryChildrenRow) []Crumb {
	out := make([]Crumb, 0, len(rows))
	for _, r := range rows {
		out = append(out, Crumb{Slug: r.Slug, Name: r.Name})
	}
	return out
}

// The shorter array wins: a mismatched length would shift every label onto the
// wrong link.
func crumbs(slugs, names []string) []Crumb {
	n := min(len(slugs), len(names))
	out := make([]Crumb, 0, n)
	for i := range n {
		out = append(out, Crumb{Slug: slugs[i], Name: names[i]})
	}
	return out
}

func brandFacets(brands []db.CategoryBrandsRow, selected map[string]bool) []FacetOption {
	out := make([]FacetOption, 0, len(brands))
	for _, b := range brands {
		out = append(out, FacetOption{
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

func tiles(rows []db.CategoryListingRow, offers map[uuid.UUID]bool) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			OnSale:       campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64),
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     photo.URL(),
			ImageSrcset:  photo.Srcset(),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
			Comparable:   offers[r.CategoryID],
		})
	}
	return out
}

func searchTiles(rows []db.SearchProductsRow, offers map[uuid.UUID]bool) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			OnSale:       campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64),
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     photo.URL(),
			ImageSrcset:  photo.Srcset(),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
			Comparable:   offers[r.CategoryID],
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

func optionFacets(rows []db.CategoryOptionValuesRow, selected []OptionFilter) []FacetGroup {
	groups := make([]FacetGroup, 0)
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
			groups = append(groups, FacetGroup{Kind: FacetVariantOption, Name: row.OptionName, Label: row.OptionLabel})
		}
		groups[position].Options = append(groups[position].Options, FacetOption{
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
