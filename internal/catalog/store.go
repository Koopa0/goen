package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/backinstock"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
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

// Listing is a category's first page under f, drawn as a grid.
func (s *Store) Listing(ctx context.Context, slug string, f Filters) (ListingView, error) {
	return s.ListingPage(ctx, slug, f, ViewGrid)
}

// ListingPage is a category's shelf under the shopper's filters: its own
// products and its descendants', with the catalogue's tree open at its
// department.
func (s *Store) ListingPage(ctx context.Context, slug string, f Filters, view View) (ListingView, error) {
	cat, err := s.q.CategoryBySlug(ctx, db.CategoryBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ListingView{}, ErrNotFound
		}
		return ListingView{}, fmt.Errorf("read category %q: %w", slug, err)
	}
	ids, err := s.q.CategoryDescendants(ctx, cat.ID)
	if err != nil {
		return ListingView{}, fmt.Errorf("read descendants of %q: %w", slug, err)
	}
	shelf, err := s.shelf(ctx, shelfScope{categoryIDs: ids, options: true}, f, view)
	if err != nil {
		return ListingView{}, fmt.Errorf("read the shelf of %q: %w", slug, err)
	}
	shelf.Path = "/c/" + slug

	trail := crumbs(cat.AncestorSlugs, cat.AncestorNames)
	department := slug
	if len(trail) > 0 {
		department = trail[0].Slug
	}
	if treeErr := s.withTree(ctx, &shelf, slug, department); treeErr != nil {
		return ListingView{}, treeErr
	}

	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return ListingView{}, err
	}
	photo := picture.Image{Digest: cat.ImageDigest, BundledKey: cat.ImageBundledKey, Width: int(cat.ImageWidth)}
	return ListingView{
		Shelf:  shelf,
		Slug:   slug,
		Name:   cat.Name,
		Crumbs: trail,
		Theme: &Theme{
			Tone:       tone.Resolve(cat.Tone),
			Comparable: offers[cat.ID],
			Photo: widget.Photo{
				URL:    photo.URL(),
				Srcset: photo.Srcset(),
				Alt:    cat.ImageAlt,
			},
		},
	}, nil
}

// ShopPage is every product on one shelf, the tree open at none.
func (s *Store) ShopPage(ctx context.Context, f Filters, view View) (ListingView, error) {
	ids, err := s.q.AllCategoryIDs(ctx)
	if err != nil {
		return ListingView{}, fmt.Errorf("read every category: %w", err)
	}
	shelf, err := s.shelf(ctx, shelfScope{categoryIDs: ids}, f, view)
	if err != nil {
		return ListingView{}, fmt.Errorf("read every product: %w", err)
	}
	shelf.Path = shopPath
	if err := s.withTree(ctx, &shelf, "", ""); err != nil {
		return ListingView{}, err
	}
	return ListingView{Shelf: shelf, Name: i18n.T(ctx, i18n.KeyShopAll)}, nil
}

const shopPath = "/shop"

// maxSearchMatches bounds the matches a search narrows and pages through, and
// so the array every query of its shelf is handed.
const maxSearchMatches = 1000

// SearchPage is a search's shelf: the products query matches, best first,
// narrowed by the shopper's filters, with how many each department holds.
// query is the term as the page echoes it (trimForDisplay's). Matches is zero,
// and nothing else is read, when the term matches nothing.
func (s *Store) SearchPage(ctx context.Context, query string, f Filters, view View) (SearchView, error) {
	terms, exact := SearchTerms(SearchPattern(query))
	keep := url.Values{"q": {query}}
	matches, err := s.q.SearchMatches(ctx, db.SearchMatchesParams{Patterns: terms, ExactPattern: exact, RowLimit: maxSearchMatches})
	if err != nil {
		return SearchView{}, fmt.Errorf("search: %w", err)
	}
	if len(matches) == 0 {
		return SearchView{Query: query, Shelf: Shelf{Path: "/search", Keep: keep, Searching: true, Filters: f, View: view, Page: 1, PageSize: PageSize}}, nil
	}
	rows, err := s.q.DepartmentTree(ctx, db.DepartmentTreeParams{
		Locale: string(i18n.FromContext(ctx)), ProductIds: matches,
	})
	if err != nil {
		return SearchView{}, fmt.Errorf("count matches by department: %w", err)
	}
	var departments []TreeLink
	for _, r := range rows {
		if r.Department && r.ProductCount > 0 {
			departments = append(departments, TreeLink{
				Slug: r.Slug, Name: r.Name, Count: r.ProductCount, Current: r.Slug == f.Department,
			})
		}
	}
	scope := shelfScope{productIDs: matches}
	if f.Department != "" && slices.ContainsFunc(departments, func(d TreeLink) bool { return d.Current }) {
		scope.categoryIDs, err = s.descendantsOf(ctx, f.Department)
	} else {
		scope.categoryIDs, err = s.q.AllCategoryIDs(ctx)
	}
	if err != nil {
		return SearchView{}, fmt.Errorf("read the departments a search spans: %w", err)
	}
	shelf, err := s.shelf(ctx, scope, f, view)
	if err != nil {
		return SearchView{}, fmt.Errorf("read the shelf of a search: %w", err)
	}
	shelf.Path = "/search"
	shelf.Keep = keep
	shelf.Searching = true
	shelf.Departments = departments
	return SearchView{Query: query, Shelf: shelf, Matches: len(matches)}, nil
}

func (s *Store) descendantsOf(ctx context.Context, slug string) ([]uuid.UUID, error) {
	cat, err := s.q.CategoryBySlug(ctx, db.CategoryBySlugParams{Slug: slug, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return nil, fmt.Errorf("read category %q: %w", slug, err)
	}
	return s.q.CategoryDescendants(ctx, cat.ID)
}

// shelfScope is the products a shelf draws from: the categories', narrowed to
// productIDs when there are any, which is also their order when the shopper
// chose none. options is whether its variants' options are offered as filters:
// within one category they are a few axes that mean the same thing, across the
// catalogue a long list that does not.
type shelfScope struct {
	categoryIDs []uuid.UUID
	productIDs  []uuid.UUID
	options     bool
}

// shelf reads one page of products under f and every count the sidebar shows
// beside them. One brand list is read first because the listing, its count and
// every facet take the chosen brands' ids from it.
func (s *Store) shelf(ctx context.Context, sc shelfScope, f Filters, view View) (Shelf, error) {
	locale := string(i18n.FromContext(ctx))
	names, values := optionFilterColumns(f.OptionValues)
	brands, err := s.q.CategoryBrands(ctx, db.CategoryBrandsParams{
		FilterVariants: f.VariantScoped(), OnSaleOnly: f.OnSaleOnly,
		OptionNames: names, OptionValues: values, InStockOnly: f.InStockOnly,
		MinPrice: f.Price.Min, MaxPrice: f.Price.Max,
		CategoryIds: sc.categoryIDs, ProductIds: sc.productIDs,
	})
	if err != nil {
		return Shelf{}, fmt.Errorf("read brands: %w", err)
	}
	brandIDs, selected := selectedListingBrands(brands, f.BrandSlugs)

	rows, err := s.q.CategoryListing(ctx, db.CategoryListingParams{
		Locale: locale, OptionNames: names, OptionValues: values,
		InStockOnly: f.InStockOnly, OnSaleOnly: f.OnSaleOnly,
		MinPrice: f.Price.Min, MaxPrice: f.Price.Max,
		CategoryIds: sc.categoryIDs, ProductIds: sc.productIDs, BrandIds: brandIDs,
		FilterVariants: f.VariantScoped(), Sort: f.Sort.Param(),
		PageSize: PageSize, PageOffset: f.Offset(),
	})
	if err != nil {
		return Shelf{}, fmt.Errorf("read listing: %w", err)
	}
	total, err := s.q.CategoryListingCount(ctx, db.CategoryListingCountParams{
		CategoryIds: sc.categoryIDs, ProductIds: sc.productIDs, BrandIds: brandIDs,
		OnSaleOnly: f.OnSaleOnly, FilterVariants: f.VariantScoped(),
		OptionNames: names, OptionValues: values, InStockOnly: f.InStockOnly,
		MinPrice: f.Price.Min, MaxPrice: f.Price.Max,
	})
	if err != nil {
		return Shelf{}, fmt.Errorf("count listing: %w", err)
	}
	bands := append([]PriceBand{{}}, priceBands...)
	mins, maxes := make([]int64, len(bands)), make([]int64, len(bands))
	for i, b := range bands {
		mins[i], maxes[i] = b.Min, b.Max
	}
	counts, err := s.q.CategoryPriceBands(ctx, db.CategoryPriceBandsParams{
		BandMins: mins, BandMaxes: maxes,
		CategoryIds: sc.categoryIDs, ProductIds: sc.productIDs, BrandIds: brandIDs,
		OnSaleOnly: f.OnSaleOnly, OptionNames: names, OptionValues: values, InStockOnly: f.InStockOnly,
	})
	if err != nil {
		return Shelf{}, fmt.Errorf("count price bands: %w", err)
	}
	offers, err := s.comparableCategories(ctx)
	if err != nil {
		return Shelf{}, err
	}

	shelf := Shelf{
		Products: tiles(rows, offers),
		Prices:   priceOptions(bands, counts, f.Price),
		Total:    total,
		Page:     int32(min(max(f.Page, 1), maxPage)),
		PageSize: PageSize,
		Filters:  f,
		View:     view,
	}
	if factErr := s.withTileFacts(ctx, shelf.Products); factErr != nil {
		return Shelf{}, factErr
	}
	if view == ViewList {
		if shelf.Specs, err = s.listSpecs(ctx, shelf.Products); err != nil {
			return Shelf{}, err
		}
	}
	if len(brands) > 0 {
		shelf.Facets = append(shelf.Facets, FacetGroup{Kind: FacetBrand, Label: i18n.T(ctx, i18n.KeyFacetBrand), Options: brandFacets(brands, selected)})
	}
	if !sc.options {
		return shelf, nil
	}
	optionRows, err := s.q.CategoryOptionValues(ctx, db.CategoryOptionValuesParams{
		Locale: locale, CategoryIds: sc.categoryIDs, ProductIds: sc.productIDs, BrandIds: brandIDs,
		InStockOnly: f.InStockOnly, OnSaleOnly: f.OnSaleOnly, MinPrice: f.Price.Min, MaxPrice: f.Price.Max,
		OptionNames: names, OptionValues: values,
	})
	if err != nil {
		return Shelf{}, fmt.Errorf("read option facets: %w", err)
	}
	shelf.Facets = append(shelf.Facets, optionFacets(optionRows, f.OptionValues)...)
	return shelf, nil
}

// priceOptions pairs each band with its count, which the query returns in the
// bands' order, one row each.
func priceOptions(bands []PriceBand, counts []db.CategoryPriceBandsRow, chosen PriceBand) []PriceOption {
	out := make([]PriceOption, len(bands))
	for i, b := range bands {
		out[i] = PriceOption{Band: b, Selected: b.ID == chosen.ID}
	}
	for _, c := range counts {
		if i := int(c.Position) - 1; i >= 0 && i < len(out) {
			out[i].Count = c.ProductCount
		}
	}
	return out
}

// withTree gives a category's or every product's shelf the catalogue's tree:
// every department that holds something, after the line for every product,
// and the sub-categories of department, the one the page is in. slug is the
// page's own category, empty on the shelf of every product.
func (s *Store) withTree(ctx context.Context, shelf *Shelf, slug, department string) error {
	rows, err := s.q.DepartmentTree(ctx, db.DepartmentTreeParams{
		Locale: string(i18n.FromContext(ctx)), Department: department, ProductIds: []uuid.UUID{},
	})
	if err != nil {
		return fmt.Errorf("read the catalogue's tree: %w", err)
	}
	every := TreeLink{Name: i18n.T(ctx, i18n.KeyShopAll), Href: shopPath, Current: slug == ""}
	shelf.Departments = []TreeLink{every}
	for _, r := range rows {
		if r.ProductCount == 0 {
			continue
		}
		link := TreeLink{Slug: r.Slug, Name: r.Name, Href: "/c/" + r.Slug, Count: r.ProductCount, Current: r.Slug == slug}
		if !r.Department {
			shelf.Subs = append(shelf.Subs, link)
			continue
		}
		shelf.Departments[0].Count += r.ProductCount
		link.Open = r.Slug == department && !link.Current
		if r.Slug == department {
			shelf.SubsTitle = r.Name
		}
		shelf.Departments = append(shelf.Departments, link)
	}
	return nil
}

// listSpecs reads the specifications each product's row in the list shows.
func (s *Store) listSpecs(ctx context.Context, products []tile.Tile) (map[string][]Spec, error) {
	slugs := make([]string, len(products))
	for i := range products {
		slugs[i] = products[i].Slug
	}
	rows, err := s.q.ListingSpecs(ctx, db.ListingSpecsParams{Slugs: slugs, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return nil, fmt.Errorf("read listing specifications: %w", err)
	}
	out := make(map[string][]Spec, len(products))
	for _, r := range rows {
		out[r.Slug] = append(out[r.Slug], Spec{Label: r.Label, Value: r.Value})
	}
	return out, nil
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

	return SearchView{Shelf: Shelf{
		Path:      "/search",
		Products:  searchTiles(rows, offers),
		Total:     total,
		Page:      int32(min(max(page, 1), maxPage)),
		PageSize:  PageSize,
		Filters:   Filters{Sort: sort},
		Searching: true,
	}}, nil
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
	return withSaleUntil(searchTiles(asSearch, offers), s.now()), nil
}

// withSaleUntil has each struck price on the shelf say the last day of its
// sale. Only the zero-result page draws the line today, so only its newest
// products are filled in.
func withSaleUntil(tiles []tile.Tile, now time.Time) []tile.Tile {
	for i := range tiles {
		if !tiles[i].SaleEndsAt.IsZero() {
			tiles[i].SaleUntil = shoptime.LastDay(tiles[i].SaleEndsAt, now)
		}
	}
	return tiles
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
		photo := picture.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:           r.Slug,
			Name:           r.Name,
			Summary:        r.Summary,
			Brand:          r.Brand,
			PriceCents:     r.MinPriceCents,
			PriceVaries:    r.PriceVaries,
			CompareCents:   r.CompareAtPriceCents.Int64,
			OnSale:         campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64),
			SaleEndsAt:     tile.SaleEnd(campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64), r.SaleEndsAt),
			Rating:         r.Rating,
			RatingCount:    r.RatingCount,
			InStock:        r.InStock,
			Colours:        r.Colours,
			ColoursSoldOut: r.ColoursSoldOut,
			ImageURL:       photo.URL(),
			ImageSrcset:    photo.Srcset(),
			ImageAlt:       r.ImageAlt,
			ImageWidth:     r.ImageWidth,
			ImageHeight:    r.ImageHeight,
			CompareControl: compareControl(offers[r.CategoryID], r.Slug, r.Name),
		})
	}
	return out
}

func searchTiles(rows []db.SearchProductsRow, offers map[uuid.UUID]bool) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := picture.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:           r.Slug,
			Name:           r.Name,
			Summary:        r.Summary,
			Brand:          r.Brand,
			PriceCents:     r.MinPriceCents,
			PriceVaries:    r.PriceVaries,
			CompareCents:   r.CompareAtPriceCents.Int64,
			OnSale:         campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64),
			SaleEndsAt:     tile.SaleEnd(campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64), r.SaleEndsAt),
			Rating:         r.Rating,
			RatingCount:    r.RatingCount,
			InStock:        r.InStock,
			Colours:        r.Colours,
			ColoursSoldOut: r.ColoursSoldOut,
			ImageURL:       photo.URL(),
			ImageSrcset:    photo.Srcset(),
			ImageAlt:       r.ImageAlt,
			ImageWidth:     r.ImageWidth,
			ImageHeight:    r.ImageHeight,
			CompareControl: compareControl(offers[r.CategoryID], r.Slug, r.Name),
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
