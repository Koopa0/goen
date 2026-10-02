// Package home renders goen's storefront home page: the carousel, the
// department cards and the product rows, read live.
package home

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
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
	bandTiles = 3
)

func (s *Store) carouselSources(ctx context.Context) ([]db.RootCategoriesRow, map[uuid.UUID][]string, []db.HomeCampaignsRow, error) {
	locale := string(i18n.FromContext(ctx))
	cats, err := s.q.RootCategories(ctx, locale)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read home categories: %w", err)
	}
	subRows, err := s.q.HomeSubcategories(ctx, locale)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read home subcategories: %w", err)
	}
	subs := make(map[uuid.UUID][]string)
	for _, r := range subRows {
		subs[r.ParentID.UUID] = append(subs[r.ParentID.UUID], r.Name)
	}
	camps, err := s.q.HomeCampaigns(ctx, db.HomeCampaignsParams{Locale: locale, MaxCampaigns: maxSlides})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read home campaigns: %w", err)
	}
	return cats, subs, camps, nil
}

// Carousel is exported so the back office lists the same slides.
func (s *Store) Carousel(ctx context.Context) ([]pages.HeroSlide, error) {
	cats, subs, camps, err := s.carouselSources(ctx)
	if err != nil {
		return nil, err
	}
	return s.slides(ctx, cats, subs, camps)
}

func (s *Store) Load(ctx context.Context) (pages.HomeView, error) {
	cats, subs, camps, err := s.carouselSources(ctx)
	if err != nil {
		return pages.HomeView{}, err
	}

	slides, err := s.slides(ctx, cats, subs, camps)
	if err != nil {
		return pages.HomeView{}, err
	}
	row, err := s.productRow(ctx, camps)
	if err != nil {
		return pages.HomeView{}, err
	}
	band, err := s.departmentBand(ctx, cats, subs)
	if err != nil {
		return pages.HomeView{}, err
	}

	freeOver, err := s.q.FreeDeliveryThreshold(ctx, !s.noPickup)
	if err != nil {
		return pages.HomeView{}, fmt.Errorf("read free delivery threshold: %w", err)
	}
	lowestFee, err := s.q.LowestDeliveryFee(ctx, !s.noPickup)
	if err != nil {
		return pages.HomeView{}, fmt.Errorf("read lowest delivery fee: %w", err)
	}

	view := pages.HomeView{
		Slides:            slides,
		Categories:        make([]pages.HomeCategory, 0, len(cats)),
		Row:               row,
		Band:              band,
		FreeDeliveryCents: freeOver,
		LowestFeeCents:    lowestFee,
		PickupOffered:     !s.noPickup,
	}
	for i := range cats {
		c := &cats[i]
		view.Categories = append(view.Categories, pages.HomeCategory{
			Slug:  c.Slug,
			Name:  c.Name,
			Tone:  pages.ResolveTone(c.Tone),
			Photo: departmentPhoto(c),
		})
	}
	return view, nil
}

func (s *Store) productRow(ctx context.Context, camps []db.HomeCampaignsRow) (pages.ProductRow, error) {
	if len(camps) > 0 {
		c := &camps[0]
		tiles, err := s.tiles(ctx, uuid.NullUUID{UUID: c.ID, Valid: true}, uuid.NullUUID{}, rowTiles)
		if err != nil {
			return pages.ProductRow{}, err
		}
		if len(tiles) > 0 {
			return pages.ProductRow{
				Title: c.Title,
				Fact:  s.campaignFact(ctx, c, i18n.KeyHomeCampaignRowFact),
				Href:  "/s/" + c.Slug,
				Tiles: tiles,
			}, nil
		}
	}
	tiles, err := s.tiles(ctx, uuid.NullUUID{}, uuid.NullUUID{}, rowTiles)
	if err != nil {
		return pages.ProductRow{}, err
	}
	return pages.ProductRow{Title: i18n.T(ctx, i18n.KeyHomeNewIn), Href: "/search", Tiles: tiles}, nil
}

// departmentBand rotates by shop day through the departments that have a
// photograph and enough products to fill the band; nil when none qualifies.
func (s *Store) departmentBand(ctx context.Context, cats []db.RootCategoriesRow, subs map[uuid.UUID][]string) (*pages.DepartmentBand, error) {
	stock, err := s.q.HomeDepartmentStock(ctx)
	if err != nil {
		return nil, fmt.Errorf("read department stock: %w", err)
	}
	held := make(map[uuid.UUID]int64, len(stock))
	for _, r := range stock {
		held[r.ID] = r.Products
	}
	var eligible []*db.RootCategoriesRow
	for i := range cats {
		if departmentPhoto(&cats[i]).Shown() && held[cats[i].ID] >= bandTiles {
			eligible = append(eligible, &cats[i])
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}

	c := eligible[dayIndex(s.now(), len(eligible))]
	tiles, err := s.tiles(ctx, uuid.NullUUID{}, uuid.NullUUID{UUID: c.ID, Valid: true}, bandTiles)
	if err != nil {
		return nil, err
	}
	return &pages.DepartmentBand{
		Name:  c.Name,
		Fact:  strings.Join(subs[c.ID], " · "),
		Href:  "/c/" + c.Slug,
		Tone:  pages.ResolveTone(c.Tone),
		Photo: departmentPhoto(c),
		Tiles: tiles,
	}, nil
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
			PriceCents:   t.MinPriceCents,
			PriceVaries:  t.PriceVaries,
			CompareCents: t.CompareAtPriceCents.Int64, // 0 when NULL
			Rating:       t.Rating,
			RatingCount:  t.RatingCount,
			InStock:      t.InStock,
			ImageURL:     assets.ProductImageURL(t.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(t.ImageKey, int(t.ImageWidth)),
			ImageAlt:     t.ImageAlt,
			ImageWidth:   t.ImageWidth,
			ImageHeight:  t.ImageHeight,
		})
	}
	return out, nil
}
