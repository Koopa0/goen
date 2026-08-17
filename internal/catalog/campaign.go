package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxCampaigns bounds how many running promotions a page lists.
const MaxCampaigns = 6

// Campaign reads a running promotion and what it features. One outside its
// window is ErrNotFound rather than an empty page.
func (s *Store) Campaign(ctx context.Context, slug string) (pages.CampaignView, error) {
	c, err := s.q.RunningCampaign(ctx, db.RunningCampaignParams{
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
		EndsAt:   c.EndsAt.Format("2006-01-02 15:04"),
		Products: campaignTiles(rows),
	}, nil
}

// RunningCampaigns is every promotion on right now, for a page that lists them.
func (s *Store) RunningCampaigns(ctx context.Context) ([]pages.CampaignSummary, error) {
	rows, err := s.q.RunningCampaigns(ctx, db.RunningCampaignsParams{
		Limit: MaxCampaigns, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read campaigns: %w", err)
	}
	out := make([]pages.CampaignSummary, 0, len(rows))
	for i := range rows {
		c := &rows[i]
		out = append(out, pages.CampaignSummary{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			EndsAt: c.EndsAt.Format("2006-01-02 15:04"),
			EndsIn: humanRemaining(ctx, time.Until(c.EndsAt)),
		})
	}
	return out, nil
}

// humanRemaining says how long a promotion has left, roughly.
func humanRemaining(ctx context.Context, d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Hour:
		return i18n.T(ctx, i18n.KeyEndsWithinHour)
	case d < 24*time.Hour:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyEndsInHours), int(d.Hours()))
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyEndsInDays), int(d.Hours()/24))
	}
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
			PriceCents:   r.MinPriceCents,
			PriceVaries:  r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out
}
