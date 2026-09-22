package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// CampaignPageSize bounds one page of running promotions.
const CampaignPageSize = 6

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
		EndsAt:   shoptime.Minute(c.EndsAt),
		Products: campaignTiles(rows),
	}, nil
}

// RunningCampaigns reads one page of promotions and the total needed to reach all of them.
func (s *Store) RunningCampaigns(ctx context.Context, page int) (pages.CampaignPage, error) {
	total, err := s.q.RunningCampaignsCount(ctx)
	if err != nil {
		return pages.CampaignPage{}, fmt.Errorf("count campaigns: %w", err)
	}
	page = max(1, min(page, maxPage, max(1, int((total+CampaignPageSize-1)/CampaignPageSize))))
	view := pages.CampaignPage{Page: page, Total: total, PageSize: CampaignPageSize}
	//nolint:gosec // G115: page is bounded to maxPage above.
	rows, err := s.q.RunningCampaigns(ctx, db.RunningCampaignsParams{
		PageSize: CampaignPageSize, PageOffset: int32((page - 1) * CampaignPageSize), Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.CampaignPage{}, fmt.Errorf("read campaigns: %w", err)
	}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, pages.CampaignSummary{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			EndsAt: shoptime.Minute(c.EndsAt), EndsIn: humanRemaining(ctx, c.RemainingSeconds),
		})
	}
	return view, nil
}

// humanRemaining keeps the database result as an integer: a schema-valid
// campaign ending centuries away overflows time.Duration.
func humanRemaining(ctx context.Context, seconds int64) string {
	switch {
	case seconds <= 0:
		return ""
	case seconds < 60*60:
		return i18n.T(ctx, i18n.KeyEndsWithinHour)
	case seconds < 24*60*60:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyEndsInHours), seconds/(60*60))
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyEndsInDays), seconds/(24*60*60))
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
			PriceVaries:  r.PriceVaries.Bool,
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
