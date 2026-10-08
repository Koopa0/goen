// Package home renders goen's storefront home page: the carousel, the
// department cards and the product rows, read live.
package home

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
)

type Store struct {
	q        *db.Queries
	noPickup bool
	// now decides the department of the day.
	now func() time.Time
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("home: NewStore requires a database handle")
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

const (
	rowTiles  = 4
	bandTiles = 4
)

// carouselSources is what the carousel and the sections beside it are read
// from; held is the number of products each department holds.
type carouselSources struct {
	cats  []db.RootCategoriesRow
	subs  map[uuid.UUID][]string
	camps []db.ListedCampaignsRow
	held  map[uuid.UUID]int64
}

func (s *Store) carouselSources(ctx context.Context) (carouselSources, error) {
	locale := string(i18n.FromContext(ctx))
	cats, err := s.q.RootCategories(ctx, locale)
	if err != nil {
		return carouselSources{}, fmt.Errorf("read home categories: %w", err)
	}
	subRows, err := s.q.HomeSubcategories(ctx, locale)
	if err != nil {
		return carouselSources{}, fmt.Errorf("read home subcategories: %w", err)
	}
	subs := make(map[uuid.UUID][]string)
	for _, r := range subRows {
		subs[r.ParentID.UUID] = append(subs[r.ParentID.UUID], r.Name)
	}
	camps, err := s.q.ListedCampaigns(ctx, db.ListedCampaignsParams{Locale: locale, PageSize: maxSlides})
	if err != nil {
		return carouselSources{}, fmt.Errorf("read home campaigns: %w", err)
	}
	stock, err := s.q.HomeDepartmentStock(ctx)
	if err != nil {
		return carouselSources{}, fmt.Errorf("read department stock: %w", err)
	}
	held := make(map[uuid.UUID]int64, len(stock))
	for _, r := range stock {
		held[r.ID] = r.Products
	}
	return carouselSources{cats: cats, subs: subs, camps: camps, held: held}, nil
}

// Carousel is exported so the back office lists the same slides.
func (s *Store) Carousel(ctx context.Context) ([]pages.HeroSlide, error) {
	src, err := s.carouselSources(ctx)
	if err != nil {
		return nil, err
	}
	return s.slides(ctx, src)
}

func (s *Store) Load(ctx context.Context) (pages.HomeView, error) {
	src, err := s.carouselSources(ctx)
	if err != nil {
		return pages.HomeView{}, err
	}

	slides, err := s.slides(ctx, src)
	if err != nil {
		return pages.HomeView{}, err
	}
	row, err := s.productRow(ctx, src.camps)
	if err != nil {
		return pages.HomeView{}, err
	}
	band, err := s.departmentBand(ctx, src, row.Tiles)
	if err != nil {
		return pages.HomeView{}, err
	}

	rules, err := catalog.ShopRules(ctx, s.q, !s.noPickup)
	if err != nil {
		return pages.HomeView{}, err
	}

	view := pages.HomeView{
		Slides:     slides,
		Categories: make([]pages.HomeCategory, 0, len(src.cats)),
		Row:        row,
		Band:       band,
		Rules:      rules,
	}
	for i := range src.cats {
		c := &src.cats[i]
		if src.held[c.ID] == 0 {
			continue
		}
		view.Categories = append(view.Categories, pages.HomeCategory{
			Slug:  c.Slug,
			Name:  c.Name,
			Tone:  pages.ResolveTone(c.Tone),
			Photo: departmentPhoto(c),
		})
	}
	return view, nil
}

func (s *Store) productRow(ctx context.Context, camps []db.ListedCampaignsRow) (pages.ProductRow, error) {
	if len(camps) > 0 {
		row, ok, err := s.campaignRow(ctx, &camps[0])
		if err != nil || ok {
			return row, err
		}
	}
	tiles, err := s.tiles(ctx, uuid.NullUUID{}, uuid.NullUUID{}, rowTiles)
	if err != nil {
		return pages.ProductRow{}, err
	}
	return pages.ProductRow{Title: i18n.T(ctx, i18n.KeyHomeNewIn), Href: "/search", Tiles: tiles}, nil
}

// campaignRow is the campaign's own row; ok is false when it has no product to show.
func (s *Store) campaignRow(ctx context.Context, c *db.ListedCampaignsRow) (row pages.ProductRow, ok bool, err error) {
	tiles, err := s.tiles(ctx, uuid.NullUUID{UUID: c.ID, Valid: true}, uuid.NullUUID{}, rowTiles)
	if err != nil || len(tiles) == 0 {
		return pages.ProductRow{}, false, err
	}
	schedule := s.campaignSchedule(ctx, c)
	campaign := &pages.RowCampaign{
		Tone:      pages.ResolveTone(c.Tone),
		Items:     c.Products,
		Facts:     schedule.Facts,
		CardFacts: schedule.CardFacts(),
	}
	if period, ok := components.DayPeriod(ctx, c.Title, c.StartsAt, c.EndsAt, s.now()); ok {
		campaign.Period = &period
	}
	return pages.ProductRow{Title: c.Title, Href: "/s/" + c.Slug, Tiles: tiles, Campaign: campaign}, true, nil
}

// departmentBand rotates by shop day through the departments with enough
// products to fill the shelf, skipping any whose shelf would be left short
// once the products already in the row above are taken out; nil when none
// qualifies.
func (s *Store) departmentBand(ctx context.Context, src carouselSources, row []pages.ProductTile) (*pages.DepartmentBand, error) {
	cats, subs, held := src.cats, src.subs, src.held
	var eligible []*db.RootCategoriesRow
	for i := range cats {
		if held[cats[i].ID] >= bandTiles {
			eligible = append(eligible, &cats[i])
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}

	start := dayIndex(s.now(), len(eligible))
	for k := range eligible {
		c := eligible[(start+k)%len(eligible)]
		tiles, err := s.tiles(ctx, uuid.NullUUID{}, uuid.NullUUID{UUID: c.ID, Valid: true}, bandTiles+rowTiles)
		if err != nil {
			return nil, err
		}
		tiles = slices.DeleteFunc(tiles, func(t pages.ProductTile) bool {
			return slices.ContainsFunc(row, func(r pages.ProductTile) bool { return r.Slug == t.Slug })
		})
		if len(tiles) < bandTiles {
			continue
		}
		return &pages.DepartmentBand{
			Name:  c.Name,
			Items: held[c.ID],
			Fact:  subCategoryLine(subs[c.ID]),
			Href:  "/c/" + c.Slug,
			Tone:  pages.ResolveTone(c.Tone),
			Tiles: tiles[:bandTiles],
		}, nil
	}
	return nil, nil
}

// subCategoryLine joins names with a no-break space before the dot, so a line
// never starts with one.
func subCategoryLine(names []string) string {
	return strings.Join(names, "\u00a0· ")
}

// dayIndex counts days since the epoch, so every visitor on one shop day sees
// the same department and the next day moves on.
func dayIndex(t time.Time, n int) int {
	day, err := time.Parse(time.DateOnly, shoptime.Day(t))
	if err != nil || n <= 0 {
		return 0
	}
	return int(day.Unix()/86400) % n
}

func (s *Store) tiles(ctx context.Context, campaign, department uuid.NullUUID, limit int32) ([]pages.ProductTile, error) {
	rows, err := s.q.HomeTiles(ctx, db.HomeTilesParams{
		Locale: string(i18n.FromContext(ctx)), CampaignID: campaign, DepartmentID: department, MaxTiles: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read home tiles: %w", err)
	}
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		t := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         t.Slug,
			Name:         t.Name,
			Summary:      t.Summary,
			Brand:        t.Brand,
			PriceCents:   t.TilePriceCents,
			PriceVaries:  t.PriceVaries,
			CompareCents: t.CompareAtPriceCents.Int64, // 0 when NULL
			InCampaign:   t.InCampaign,
			Rating:       t.Rating,
			RatingCount:  t.RatingCount,
			InStock:      t.InStock,
			Colours:      t.Colours,
			ImageURL:     assets.ProductImageURL(t.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(t.ImageKey, int(t.ImageWidth)),
			ImageAlt:     t.ImageAlt,
			ImageWidth:   t.ImageWidth,
			ImageHeight:  t.ImageHeight,
		})
	}
	return out, nil
}
