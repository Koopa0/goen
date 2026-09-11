package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxCampaignDays bounds how long one promotion may run.
const MaxCampaignDays = 90

// MaxCampaignTitleRunes bounds the heading a shopper reads.
const MaxCampaignTitleRunes = 60

// CampaignForm is what the back office submits.
type CampaignForm struct {
	Slug  string
	Title string
	Days  int32
}

// Validate refuses what the schema would, and the window the shop should refuse.
func (f *CampaignForm) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Title = strings.TrimSpace(f.Title)

	errs := map[string]string{}
	if !slugFormat.MatchString(f.Slug) {
		errs["slug"] = i18n.T(ctx, i18n.KeyFormSlugFormat)
	}
	if f.Title == "" || utf8.RuneCountInString(f.Title) > MaxCampaignTitleRunes {
		errs["title"] = i18n.T(ctx, i18n.KeyFormCampaignTitle)
	}
	if f.Days < 1 || f.Days > MaxCampaignDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormCampaignDays)
	}
	return errs
}

// Campaigns reads the promotions for the back office.
func (s *Store) Campaigns(ctx context.Context) (pages.AdminCampaignsView, error) {
	rows, err := s.q.AdminCampaigns(ctx, PageSize)
	if err != nil {
		return pages.AdminCampaignsView{}, fmt.Errorf("read campaigns: %w", err)
	}
	view := pages.AdminCampaignsView{}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, pages.AdminCampaign{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			Active: c.IsActive, Running: c.IsRunning,
			StartsAt: shoptime.Day(c.StartsAt),
			EndsAt:   shoptime.Minute(c.EndsAt),
		})
	}
	return view, nil
}

// CreateCampaign starts a promotion, with nothing featured.
func (s *Store) CreateCampaign(ctx context.Context, f *CampaignForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: actionCreateCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": f.Slug, "title": f.Title, "days": f.Days},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCampaign(ctx, db.CreateCampaignParams{
				Slug: f.Slug, Title: f.Title, Days: f.Days,
			})
		})
	if err != nil {
		if hasConstraint(err, "sale_campaigns_slug_key") {
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenCampaign)}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// SetCampaignActive switches a promotion on or off.
func (s *Store) SetCampaignActive(ctx context.Context, slug string, active bool) error {
	return s.audited(ctx, Event{
		Action: actionToggleCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"slug": slug}, After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignActive(ctx, db.SetCampaignActiveParams{
				Slug: strings.TrimSpace(slug), IsActive: active,
			})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// FeatureProduct adds a product; sale_campaign_needs_discount decides eligibility.
func (s *Store) FeatureProduct(ctx context.Context, campaign, product string) error {
	return s.audited(ctx, Event{
		Action: actionFeatureProduct, Table: "sale_campaign_products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"campaign": campaign, "product": product},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.AddCampaignProduct(ctx, db.AddCampaignProductParams{
				Campaign: strings.TrimSpace(campaign), Product: strings.TrimSpace(product),
			})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// UnfeatureProduct removes one.
func (s *Store) UnfeatureProduct(ctx context.Context, campaign, product string) error {
	return s.audited(ctx, Event{
		Action: actionUnfeatureProduct, Table: "sale_campaign_products",
		Before: map[string]any{"campaign": campaign, "product": product},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.RemoveCampaignProduct(ctx, db.RemoveCampaignProductParams{
				Campaign: strings.TrimSpace(campaign), Product: strings.TrimSpace(product),
			}); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// CampaignProducts is what one campaign features.
func (s *Store) CampaignProducts(ctx context.Context, slug string) ([]pages.AdminCampaignProduct, error) {
	rows, err := s.q.AdminCampaignProducts(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read campaign products: %w", err)
	}
	out := make([]pages.AdminCampaignProduct, 0, len(rows))
	for i := range rows {
		out = append(out, pages.AdminCampaignProduct{
			Slug: rows[i].Slug, Name: rows[i].Name,
		})
	}
	return out, nil
}
