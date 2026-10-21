package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"

	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/tile"
)

const CampaignPageSize = 6

// Campaign returns ErrNotFound for a campaign that is switched off. One outside
// its window is still shown, as not started or ended.
func (s *Store) Campaign(ctx context.Context, slug string) (CampaignView, error) {
	c, err := s.q.CampaignBySlug(ctx, db.CampaignBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CampaignView{}, ErrNotFound
		}
		return CampaignView{}, fmt.Errorf("read campaign %q: %w", slug, err)
	}

	rows, err := s.q.CampaignProducts(ctx, db.CampaignProductsParams{
		CampaignID: c.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return CampaignView{}, fmt.Errorf("read campaign products: %w", err)
	}
	photo := assets.Image{Digest: c.ImageDigest, BundledKey: c.ImageBundledKey, Width: int(c.ImageWidth)}
	return CampaignView{
		Slug:     c.Slug,
		Title:    c.Title,
		Schedule: campaign.NewSchedule(ctx, c.Title, int64(len(rows)), c.StartsAt, c.EndsAt, s.now()),
		Products: campaignTiles(rows),
		Image: components.Photo{
			URL:    photo.URL(),
			Srcset: photo.Srcset(),
			Alt:    c.ImageAlt,
		},
		Tone: components.ResolveTone(c.Tone),
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

func (s *Store) ListedCampaigns(ctx context.Context, page int) (CampaignPage, error) {
	total, err := s.q.ListedCampaignsCount(ctx)
	if err != nil {
		return CampaignPage{}, fmt.Errorf("count campaigns: %w", err)
	}
	page = max(1, min(page, maxPage, max(1, int((total+CampaignPageSize-1)/CampaignPageSize))))
	view := CampaignPage{Page: page, Total: total, PageSize: CampaignPageSize}
	rows, err := s.q.ListedCampaigns(ctx, db.ListedCampaignsParams{
		PageSize: CampaignPageSize, PageOffset: int32((page - 1) * CampaignPageSize), Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return CampaignPage{}, fmt.Errorf("read campaigns: %w", err)
	}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, CampaignSummary{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			EndsOn: campaign.EndsOn(ctx, c.EndsAt, s.now()),
		})
	}
	return view, nil
}

func campaignTiles(rows []db.CampaignProductsRow) []tile.Tile {
	out := make([]tile.Tile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, tile.Tile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.TilePriceCents,
			PriceVaries:  r.PriceVaries.Bool,
			CompareCents: r.CompareAtPriceCents.Int64,
			OnSale:       campaign.OnSale(r.InCampaign, r.InStock, r.TilePriceCents, r.CompareAtPriceCents.Int64),
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
