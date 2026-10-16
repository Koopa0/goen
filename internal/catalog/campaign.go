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
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
)

const CampaignPageSize = 6

// CampaignState is where a campaign's window lies against the present; the zero value is running.
type CampaignState string

const (
	CampaignRunning    CampaignState = ""
	CampaignNotStarted CampaignState = "not-started"
	CampaignEnded      CampaignState = "ended"
)

// CampaignStateAt judges the window [startsAt, endsAt) at now, the window the
// running_campaigns view holds a switched-on campaign inside at the database's
// now().
func CampaignStateAt(startsAt, endsAt, now time.Time) CampaignState {
	switch {
	case now.Before(startsAt):
		return CampaignNotStarted
	case !now.Before(endsAt):
		return CampaignEnded
	}
	return CampaignRunning
}

// OnSale reports whether a price is shown struck from its compare-at price: only
// while a listed campaign (one running with a deal) features the product, only
// for what can be bought, and only when the compare-at price is the higher. It
// asks of the one variant a card or a page prices what the campaign_deals view
// asks of some variant of a campaign's product.
func OnSale(featured, buyable bool, priceCents, compareCents int64) bool {
	return buyable && featured && compareCents > priceCents
}

// NewCampaignSchedule states a campaign of items products running from startsAt
// to endsAt, which is exclusive, as it stands at now.
func NewCampaignSchedule(ctx context.Context, title string, items int64, startsAt, endsAt, now time.Time) pages.CampaignSchedule {
	state := CampaignStateAt(startsAt, endsAt, now)
	count := components.Stat{Label: i18n.T(ctx, i18n.KeySlideItems), Value: components.StatCount(items, i18n.T(ctx, i18n.KeyFactUnitItems))}
	ends := pages.CampaignEndStat(ctx, endsAt, now)
	schedule := pages.CampaignSchedule{Ends: ends}

	switch state {
	case CampaignNotStarted:
		first := shoptime.DateOf(startsAt, now)
		starts := components.Stat{
			Label: i18n.T(ctx, i18n.KeyCampaignStarts),
			Value: components.StatDate(shoptime.DateText(ctx, first), "").WithDatetime(first.ISO()),
		}
		schedule.Ends.Note = ""
		schedule.Facts = []components.Stat{starts, schedule.Ends, count}
	case CampaignEnded:
		schedule.Ends.Note = i18n.T(ctx, i18n.KeyCampaignEnded)
		schedule.Facts = []components.Stat{schedule.Ends, count}
	default:
		// The last two days say so in the end's note instead.
		if left := shoptime.DaysLeft(now, endsAt); left > 1 {
			schedule.DaysLeft = components.Stat{Label: i18n.T(ctx, i18n.KeySlideDaysLeft), Value: components.StatCount(int64(left), i18n.T(ctx, i18n.KeyFactUnitDays))}
		}
		schedule.Facts = []components.Stat{count, schedule.Ends}
		if schedule.DaysLeft.Label != "" {
			schedule.Facts = append(schedule.Facts, schedule.DaysLeft)
		}
	}
	schedule.Period, schedule.HasPeriod = components.DayPeriod(ctx, title, startsAt, endsAt, now)
	return schedule
}

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
	photo := assets.Image{Digest: c.ImageDigest, BundledKey: c.ImageBundledKey, Width: int(c.ImageWidth)}
	return pages.CampaignView{
		Slug:     c.Slug,
		Title:    c.Title,
		Schedule: NewCampaignSchedule(ctx, c.Title, int64(len(rows)), c.StartsAt, c.EndsAt, s.now()),
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
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		out = append(out, pages.ProductTile{
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
