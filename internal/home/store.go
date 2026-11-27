// Package home renders goen's storefront home page: the bento hero, the
// largest department's shelf and stage, the rail of cheap products, the other
// departments and the shop's rules, read live.
package home

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Store struct {
	q *db.Queries
	// site reads the departments, which the header lists in the same order.
	site *site.Store
	// policy reads the shop rules the page shows.
	policy *policy.Store
	// now dates a campaign's end and a sale's last day.
	now func() time.Time
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("home: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx), site: site.NewStore(dbtx), policy: policy.NewStore(dbtx), now: time.Now}
}

// WithoutPickup is for a deployment whose store map is not configured: checkout
// offers no pickup there, so what this store describes must not either.
func (s *Store) WithoutPickup() *Store {
	c := *s
	c.policy = s.policy.WithoutPickup()
	return &c
}

const (
	shelfTiles = 8
	stageTiles = 2
	railTiles  = 12
	moreTiles  = 4

	// railBelowCents is the rail's ceiling, NT$1,000: the price of something
	// added to a basket on the way to free delivery rather than a purchase of
	// its own. It is the home page's merchandising choice, not a rule the shop
	// enforces anywhere, so it lives here and nowhere else.
	railBelowCents = 100000

	// lifestyleDepartment is the department whose goods the bundled lifestyle
	// photograph shows (stoneware, linen, tea): the tile leads there when no
	// slide or campaign takes it. A shop without it, or with nothing in it,
	// leads to its largest other department instead.
	lifestyleDepartment = "home-living"
)

// sources is what every part of the page is read from: the departments in the
// header's order with the products each holds, their sub-categories' names,
// and the running campaign that ends first.
type sources struct {
	cats  []db.RootCategoriesRow
	held  map[uuid.UUID]int64
	subs  map[uuid.UUID][]string
	camps []db.ListedCampaignsRow
}

func (s *Store) sources(ctx context.Context) (sources, error) {
	locale := string(i18n.FromContext(ctx))
	cats, held, err := s.site.Departments(ctx)
	if err != nil {
		return sources{}, fmt.Errorf("read home departments: %w", err)
	}
	subRows, err := s.q.HomeSubcategories(ctx, locale)
	if err != nil {
		return sources{}, fmt.Errorf("read home subcategories: %w", err)
	}
	subs := make(map[uuid.UUID][]string)
	for _, r := range subRows {
		subs[r.ParentID.UUID] = append(subs[r.ParentID.UUID], r.Name)
	}
	camps, err := s.q.ListedCampaigns(ctx, db.ListedCampaignsParams{Locale: locale, PageSize: 1})
	if err != nil {
		return sources{}, fmt.Errorf("read home campaigns: %w", err)
	}
	return sources{cats: cats, held: held, subs: subs, camps: camps}, nil
}

// featured is the department holding the most products, the first of equals
// in the header's order; nil when no department holds any.
func (src *sources) featured() *db.RootCategoriesRow {
	var best *db.RootCategoriesRow
	for i := range src.cats {
		c := &src.cats[i]
		if src.held[c.ID] > 0 && (best == nil || src.held[c.ID] > src.held[best.ID]) {
			best = c
		}
	}
	return best
}

// others are the departments with products other than the featured one, in
// the header's order.
func (src *sources) others(featured *db.RootCategoriesRow) []*db.RootCategoriesRow {
	var out []*db.RootCategoriesRow
	for i := range src.cats {
		c := &src.cats[i]
		if src.held[c.ID] > 0 && (featured == nil || c.ID != featured.ID) {
			out = append(out, c)
		}
	}
	return out
}

func (src *sources) category(c *db.RootCategoriesRow) Category {
	return Category{Slug: c.Slug, Name: c.Name, Products: src.held[c.ID]}
}

func (src *sources) products() int64 {
	var n int64
	for _, held := range src.held {
		n += held
	}
	return n
}

// Load reads the page for a visitor's choice. A choice that names nothing the
// page offers is the default.
func (s *Store) Load(ctx context.Context, choice Choice) (View, error) {
	src, err := s.sources(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{Products: src.products()}

	featured := src.featured()
	lifestyle, err := s.lifestyle(ctx, &src, featured)
	if err != nil {
		return View{}, err
	}
	view.Lifestyle = lifestyle

	if featured != nil {
		dept := src.category(featured)
		view.Featured = &dept
		if view.Shelf, err = s.shelf(ctx, featured, dept, choice.Shelf); err != nil {
			return View{}, err
		}
		stage, stageErr := s.tiles(ctx, tileFilter{department: featured.ID, inStock: true, order: "desc"}, stageTiles)
		if stageErr != nil {
			return View{}, stageErr
		}
		if len(stage) == stageTiles {
			view.Stage = s.saleUntil(stage)
		}
	}

	rail, err := s.tiles(ctx, tileFilter{below: railBelowCents, order: "asc"}, railTiles)
	if err != nil {
		return View{}, err
	}
	view.Rail = Rail{BelowCents: railBelowCents, Tiles: rail}

	if view.More, err = s.more(ctx, &src, featured, choice.More); err != nil {
		return View{}, err
	}
	view.Choice = honoured(&view, choice)

	if view.Rules, err = s.policy.Rules(ctx); err != nil {
		return View{}, err
	}
	return view, nil
}

// honoured is the visitor's choice as far as the page followed it: a pill's
// link carries on only what the page shows, and the default is the page
// without one.
func honoured(v *View, choice Choice) Choice {
	var c Choice
	if v.Shelf != nil {
		c.Shelf = v.Shelf.Chosen
	}
	if v.More != nil && v.More.Chosen.Slug == choice.More {
		c.More = choice.More
	}
	return c
}

// shelf is the featured department's products, or one of its sub-categories'
// where the visitor chose one that holds products.
func (s *Store) shelf(ctx context.Context, featured *db.RootCategoriesRow, dept Category, chosen string) (*Shelf, error) {
	rows, err := s.q.HomeShelfCategories(ctx, db.HomeShelfCategoriesParams{
		DepartmentID: featured.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read the featured shelf's categories: %w", err)
	}
	shelf := &Shelf{Department: dept}
	from := featured.ID
	for _, r := range rows {
		if r.Products == 0 {
			continue
		}
		shelf.Filters = append(shelf.Filters, Category{Slug: r.Slug, Name: r.Name, Products: r.Products})
		if r.Slug == chosen {
			shelf.Chosen, from = r.Slug, r.ID
		}
	}
	tiles, err := s.tiles(ctx, tileFilter{department: from}, shelfTiles)
	if err != nil {
		return nil, err
	}
	shelf.Tiles = s.saleUntil(tiles)
	if shelf.Compares, err = s.q.ShelfCompares(ctx, from); err != nil {
		return nil, fmt.Errorf("read whether the featured shelf compares: %w", err)
	}
	return shelf, nil
}

// more is the showcase of the departments other than the featured one: the
// one the visitor chose, or the first.
func (s *Store) more(ctx context.Context, src *sources, featured *db.RootCategoriesRow, chosen string) (*More, error) {
	others := src.others(featured)
	if len(others) == 0 {
		return nil, nil
	}
	pick := others[0]
	more := &More{}
	for _, c := range others {
		more.Tabs = append(more.Tabs, src.category(c))
		if c.Slug == chosen {
			pick = c
		}
	}
	more.Chosen = src.category(pick)
	more.Photo = departmentPhoto(pick)
	more.Subs = src.subs[pick.ID]
	tiles, err := s.tiles(ctx, tileFilter{department: pick.ID}, moreTiles)
	if err != nil {
		return nil, err
	}
	more.Tiles = s.saleUntil(tiles)
	return more, nil
}

func departmentPhoto(c *db.RootCategoriesRow) widget.Photo {
	photo := picture.Image{Digest: c.ImageDigest, BundledKey: c.ImageBundledKey, Width: int(c.ImageWidth)}
	url := photo.URL()
	if url == "" {
		return widget.Photo{}
	}
	return widget.Photo{URL: url, Srcset: photo.Srcset(), Alt: c.ImageAlt}
}

// saleUntil has each struck price on a shelf say the last day of its sale.
func (s *Store) saleUntil(tiles []tile.Tile) []tile.Tile {
	now := s.now()
	for i := range tiles {
		if !tiles[i].SaleEndsAt.IsZero() {
			tiles[i].SaleUntil = shoptime.LastDay(tiles[i].SaleEndsAt, now)
		}
	}
	return tiles
}

// tileFilter narrows the tile query: a department and everything under it,
// what can be bought, or what costs less than a ceiling; order sorts by price,
// "asc" or "desc", within the products that can be bought and have a photo.
type tileFilter struct {
	department uuid.UUID
	below      int64
	inStock    bool
	order      string
}

func (s *Store) tiles(ctx context.Context, f tileFilter, limit int32) ([]tile.Tile, error) {
	rows, err := s.q.HomeTiles(ctx, db.HomeTilesParams{
		Locale:       string(i18n.FromContext(ctx)),
		DepartmentID: uuid.NullUUID{UUID: f.department, Valid: f.department != uuid.Nil},
		BelowCents:   f.below,
		InStockOnly:  f.inStock,
		PriceOrder:   f.order,
		MaxTiles:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read home tiles: %w", err)
	}
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		t := &rows[i]
		onSale := campaign.OnSale(t.InCampaign, t.InStock, t.TilePriceCents, t.CompareAtPriceCents.Int64)
		photo := picture.Image{Digest: t.ImageDigest, BundledKey: t.ImageBundledKey, Width: int(t.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:           t.Slug,
			Name:           t.Name,
			Summary:        t.Summary,
			Brand:          t.Brand,
			PriceCents:     t.TilePriceCents,
			PriceVaries:    t.PriceVaries,
			CompareCents:   t.CompareAtPriceCents.Int64, // 0 when NULL
			OnSale:         onSale,
			SaleEndsAt:     tile.SaleEnd(onSale, t.SaleEndsAt),
			Rating:         t.Rating,
			RatingCount:    t.RatingCount,
			InStock:        t.InStock,
			Colours:        t.Colours,
			ColoursSoldOut: t.ColoursSoldOut,
			ColourNames:    t.ColourNames,
			ColourPhotos:   tile.ColourPhotos(t.ColourDigests, t.ColourBundledKeys, t.ColourWidths),
			ImageURL:       photo.URL(),
			ImageSrcset:    photo.Srcset(),
			ImageAlt:       t.ImageAlt,
			ImageWidth:     t.ImageWidth,
			ImageHeight:    t.ImageHeight,
			Highlights:     t.Highlights,
			WarrantyMonths: t.WarrantyMonths,
			QuickAdd:       t.QuickAdd,
		})
	}
	return out, nil
}
