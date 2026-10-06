package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

const CampaignPageSize = 6

// Campaign returns ErrNotFound for a campaign that is switched off. One outside
// its window is still shown, as not started or ended.
func (s *Store) Campaign(ctx context.Context, slug string) (pages.CampaignView, error) {
	c, err := s.q.CampaignBySlug(ctx, db.CampaignBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.CampaignView{}, ErrNotFound
		}
		return pages.CampaignView{}, fmt.Errorf("read campaign %q: %w", slug, err)
	}

	rows, err := s.q.CampaignProducts(ctx, db.CampaignProductsParams{
		CampaignID: c.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CampaignView{}, fmt.Errorf("read campaign products: %w", err)
	}
	return pages.CampaignView{
		Slug:     c.Slug,
		Title:    c.Title,
		Schedule: pages.NewCampaignSchedule(ctx, c.Title, int64(len(rows)), c.StartsAt, c.EndsAt, s.now()),
		Products: campaignTiles(rows),
		Image: pages.Photo{
			URL:    assets.ProductImageURL(c.ImageKey),
			Srcset: assets.ProductImageSrcsetAt(c.ImageKey, int(c.ImageWidth)),
			Alt:    c.ImageAlt,
		},
		Tone: pages.ResolveTone(c.Tone),
	}, nil
}

// DealsOnOffer is whether /deals has a product to buy or a campaign to list.
func (s *Store) DealsOnOffer(ctx context.Context) (bool, error) {
	offered, err := s.q.DealsHaveSomethingToBuy(ctx)
	if err != nil {
		return false, fmt.Errorf("read whether deals are on offer: %w", err)
	}
	return offered, nil
}

func (s *Store) ListedCampaigns(ctx context.Context, page int) (pages.CampaignPage, error) {
	total, err := s.q.ListedCampaignsCount(ctx)
	if err != nil {
		return pages.CampaignPage{}, fmt.Errorf("count campaigns: %w", err)
	}
	page = max(1, min(page, maxPage, max(1, int((total+CampaignPageSize-1)/CampaignPageSize))))
	view := pages.CampaignPage{Page: page, Total: total, PageSize: CampaignPageSize}
	rows, err := s.q.ListedCampaigns(ctx, db.ListedCampaignsParams{
		PageSize: CampaignPageSize, PageOffset: int32((page - 1) * CampaignPageSize), Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CampaignPage{}, fmt.Errorf("read campaigns: %w", err)
	}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, pages.CampaignSummary{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			EndsOn: pages.CampaignEndsOn(ctx, c.EndsAt, s.now()),
		})
	}
	return view, nil
}

func campaignTiles(rows []db.CampaignProductsRow) []pages.ProductTile {
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.TilePriceCents,
			PriceVaries:  r.PriceVaries.Bool,
			CompareCents: r.CompareAtPriceCents.Int64,
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
