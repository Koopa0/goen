// Package home renders goen's storefront home page: the carousel, the
// department cards and the product rows, read live.
package home

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
)

type Store struct {
	q *db.Queries
	// site reads the departments, which the header lists in the same order.
	site *site.Store
	// policy reads the shop rules the page shows.
	policy *policy.Store
	// now decides the department of the day.
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
	cats, held, err := s.site.Departments(ctx)
	if err != nil {
		return carouselSources{}, fmt.Errorf("read home departments: %w", err)
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
	return carouselSources{cats: cats, subs: subs, camps: camps, held: held}, nil
}

// Carousel is exported so the back office lists the same slides: cmd/goen
// hands this store to the slides desk as its slides.Storefront.
func (s *Store) Carousel(ctx context.Context) ([]hero.Slide, error) {
	src, err := s.carouselSources(ctx)
	if err != nil {
		return nil, err
	}
	return s.slides(ctx, src)
}

func (s *Store) Load(ctx context.Context) (View, error) {
	src, err := s.carouselSources(ctx)
	if err != nil {
		return View{}, err
	}

	slides, err := s.slides(ctx, src)
	if err != nil {
		return View{}, err
	}
	row, err := s.productRow(ctx, src.camps)
	if err != nil {
		return View{}, err
	}
	band, err := s.departmentBand(ctx, src, row.Tiles)
	if err != nil {
		return View{}, err
	}

	rules, err := s.policy.Rules(ctx)
	if err != nil {
		return View{}, err
	}

	return View{Slides: slides, Row: row, Band: band, Rules: rules}, nil
}

func (s *Store) productRow(ctx context.Context, camps []db.ListedCampaignsRow) (ProductRow, error) {
	if len(camps) > 0 {
		row, ok, err := s.campaignRow(ctx, &camps[0])
		if err != nil || ok {
			return row, err
		}
	}
	tiles, err := s.tiles(ctx, uuid.NullUUID{}, uuid.NullUUID{}, rowTiles)
	if err != nil {
		return ProductRow{}, err
	}
	return ProductRow{Title: i18n.T(ctx, i18n.KeyHomeNewIn), Href: "/search", Tiles: tiles}, nil
}

// campaignRow is the campaign's own row; ok is false when it has no product to show.
func (s *Store) campaignRow(ctx context.Context, c *db.ListedCampaignsRow) (row ProductRow, ok bool, err error) {
	tiles, err := s.tiles(ctx, uuid.NullUUID{UUID: c.ID, Valid: true}, uuid.NullUUID{}, rowTiles)
	if err != nil || len(tiles) == 0 {
		return ProductRow{}, false, err
	}
	rowCampaign := &RowCampaign{Items: c.Products, End: campaign.NewEnd(c.EndsAt, s.now())}
	return ProductRow{Title: c.Title, Href: "/s/" + c.Slug, Tiles: tiles, Campaign: rowCampaign}, true, nil
}

// departmentBand rotates by shop day through the departments with enough
// products to fill the shelf, skipping any whose shelf would be left short
// once the products already in the row above are taken out; nil when none
// qualifies.
func (s *Store) departmentBand(ctx context.Context, src carouselSources, row []tile.Tile) (*DepartmentBand, error) {
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
		tiles = slices.DeleteFunc(tiles, func(t tile.Tile) bool {
			return slices.ContainsFunc(row, func(r tile.Tile) bool { return r.Slug == t.Slug })
		})
		if len(tiles) < bandTiles {
			continue
		}
		return &DepartmentBand{
			Name:  c.Name,
			Items: held[c.ID],
			Subs:  subs[c.ID],
			Href:  "/c/" + c.Slug,
			Tone:  tone.Resolve(c.Tone),
			Tiles: s.saleUntil(tiles[:bandTiles]),
		}, nil
	}
	return nil, nil
}

// saleUntil has each struck price on the shelf say the last day of its sale.
func (s *Store) saleUntil(tiles []tile.Tile) []tile.Tile {
	now := s.now()
	for i := range tiles {
		if !tiles[i].SaleEndsAt.IsZero() {
			tiles[i].SaleUntil = shoptime.LastDay(tiles[i].SaleEndsAt, now)
		}
	}
	return tiles
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

func (s *Store) tiles(ctx context.Context, campaignID, department uuid.NullUUID, limit int32) ([]tile.Tile, error) {
	rows, err := s.q.HomeTiles(ctx, db.HomeTilesParams{
		Locale: string(i18n.FromContext(ctx)), CampaignID: campaignID, DepartmentID: department, MaxTiles: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("read home tiles: %w", err)
	}
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		t := &rows[i]
		photo := picture.Image{Digest: t.ImageDigest, BundledKey: t.ImageBundledKey, Width: int(t.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:           t.Slug,
			Name:           t.Name,
			Summary:        t.Summary,
			Brand:          t.Brand,
			PriceCents:     t.TilePriceCents,
			PriceVaries:    t.PriceVaries,
			CompareCents:   t.CompareAtPriceCents.Int64, // 0 when NULL
			OnSale:         campaign.OnSale(t.InCampaign, t.InStock, t.TilePriceCents, t.CompareAtPriceCents.Int64),
			SaleEndsAt:     tile.SaleEnd(campaign.OnSale(t.InCampaign, t.InStock, t.TilePriceCents, t.CompareAtPriceCents.Int64), t.SaleEndsAt),
			Rating:         t.Rating,
			RatingCount:    t.RatingCount,
			InStock:        t.InStock,
			Colours:        t.Colours,
			ColoursSoldOut: t.ColoursSoldOut,
			ImageURL:       photo.URL(),
			ImageSrcset:    photo.Srcset(),
			ImageAlt:       t.ImageAlt,
			ImageWidth:     t.ImageWidth,
			ImageHeight:    t.ImageHeight,
		})
	}
	return out, nil
}
