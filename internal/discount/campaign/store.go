package campaign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

// ErrNotFound is a campaign that does not exist or is switched off.
var ErrNotFound = errors.New("campaign: not found")

// ListedPageSize is how many campaigns /deals lists on one page of its strip.
const ListedPageSize = 6

// Store reads the campaign page, /deals and whether /deals has anything to buy.
type Store struct {
	q   *db.Queries
	now func() time.Time
}

func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("campaign: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx), now: time.Now}
}

// Campaign returns ErrNotFound for a campaign that is switched off. One outside
// its window is still shown, as not started or ended.
func (s *Store) Campaign(ctx context.Context, slug string) (View, error) {
	c, err := s.q.CampaignBySlug(ctx, db.CampaignBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrNotFound
		}
		return View{}, fmt.Errorf("read campaign %q: %w", slug, err)
	}

	rows, err := s.q.CampaignProducts(ctx, db.CampaignProductsParams{
		CampaignID: c.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return View{}, fmt.Errorf("read campaign products: %w", err)
	}
	photo := picture.Image{Digest: c.ImageDigest, BundledKey: c.ImageBundledKey, Width: int(c.ImageWidth)}
	return View{
		Slug:     c.Slug,
		Title:    c.Title,
		Schedule: NewSchedule(ctx, c.Title, int64(len(rows)), c.StartsAt, c.EndsAt, s.now()),
		Products: campaignTiles(rows),
		Image: widget.Photo{
			URL:    photo.URL(),
			Srcset: photo.Srcset(),
			Alt:    c.ImageAlt,
		},
		Tone: tone.Resolve(c.Tone),
	}, nil
}

// DealsOnOffer is whether a running campaign has something to buy, which is what /deals lists.
func (s *Store) DealsOnOffer(ctx context.Context) (bool, error) {
	offered, err := s.q.DealsHaveSomethingToBuy(ctx)
	if err != nil {
		return false, fmt.Errorf("read whether deals are on offer: %w", err)
	}
	return offered, nil
}

// Listed is the given page of the campaigns /deals lists, clamped to the last.
func (s *Store) Listed(ctx context.Context, page int) (Listed, error) {
	total, err := s.q.ListedCampaignsCount(ctx)
	if err != nil {
		return Listed{}, fmt.Errorf("count campaigns: %w", err)
	}
	page = max(1, min(page, maxPage, max(1, int((total+ListedPageSize-1)/ListedPageSize))))
	view := Listed{Page: page, Total: total, PageSize: ListedPageSize}
	rows, err := s.q.ListedCampaigns(ctx, db.ListedCampaignsParams{
		PageSize: ListedPageSize, PageOffset: int32((page - 1) * ListedPageSize), Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return Listed{}, fmt.Errorf("read campaigns: %w", err)
	}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, Summary{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			EndsOn: EndsOn(ctx, c.EndsAt, s.now()),
		})
	}
	return view, nil
}

// Deals is the given page of the products running campaigns discount.
func (s *Store) Deals(ctx context.Context, page int) (DealsView, error) {
	rows, err := s.q.DealProducts(ctx, db.DealProductsParams{
		Locale:     string(i18n.FromContext(ctx)),
		PageSize:   DealsPageSize,
		PageOffset: offsetFor(page),
	})
	if err != nil {
		return DealsView{}, fmt.Errorf("read deals: %w", err)
	}
	total, err := s.q.DealProductsCount(ctx)
	if err != nil {
		return DealsView{}, fmt.Errorf("count deals: %w", err)
	}
	return DealsView{
		Products: dealTiles(rows),
		Total:    total,
		Page:     max(page, 1),
		PageSize: DealsPageSize,
	}, nil
}

func campaignTiles(rows []db.CampaignProductsRow) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := picture.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.TilePriceCents,
			PriceVaries:  r.PriceVaries.Bool,
			CompareCents: r.CompareAtPriceCents.Int64,
			OnSale:       OnSale(r.InCampaign, r.InStock, r.TilePriceCents, r.CompareAtPriceCents.Int64),
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     photo.URL(),
			ImageSrcset:  photo.Srcset(),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out
}

// dealTiles exists because sqlc emits a row struct per query, so one function
// cannot take both.
func dealTiles(rows []db.DealProductsRow) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := picture.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.TilePriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			OnSale:       OnSale(r.InCampaign, r.InStock, r.TilePriceCents, r.CompareAtPriceCents.Int64),
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     photo.URL(),
			ImageSrcset:  photo.Srcset(),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out
}
