package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxCampaignDays bounds how long one promotion may run.
//
// Ninety days. Not a schema rule — sale_campaigns takes any window — but a
// "limited-time" offer that runs for a year is a price, and a form that lets
// somebody type 3650 by accident is a form that eventually does.
const MaxCampaignDays = 90

// MaxCampaignTitleRunes bounds the heading a shopper reads.
const MaxCampaignTitleRunes = 60

// CampaignForm is what the back office submits.
type CampaignForm struct {
	Slug  string
	Title string
	Days  int32
}

// Validate refuses what the schema would, and the window the shop should.
func (f *CampaignForm) Validate() map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Title = strings.TrimSpace(f.Title)

	errs := map[string]string{}
	if !slugFormat.MatchString(f.Slug) {
		errs["slug"] = "網址代稱只能用小寫英數與連字號。"
	}
	if f.Title == "" || utf8.RuneCountInString(f.Title) > MaxCampaignTitleRunes {
		errs["title"] = "請填寫活動標題,不超過 60 個字。"
	}
	if f.Days < 1 || f.Days > MaxCampaignDays {
		errs["days"] = "活動天數必須介於 1 到 90 天。"
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
			StartsAt: c.StartsAt.Format("2006-01-02"),
			EndsAt:   c.EndsAt.Format("2006-01-02 15:04"),
		})
	}
	return view, nil
}

// CreateCampaign starts a promotion.
//
// It begins with nothing featured, and that is not an oversight: a campaign is
// a curation, and creating one pre-filled would be the software deciding what
// is on offer.
func (s *Store) CreateCampaign(ctx context.Context, f *CampaignForm) (map[string]string, error) {
	if errs := f.Validate(); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: ActionCreateCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": f.Slug, "title": f.Title, "days": f.Days},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCampaign(ctx, db.CreateCampaignParams{
				Slug: f.Slug, Title: f.Title, Days: f.Days,
			})
		})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "sale_campaigns_slug_key" {
			return map[string]string{"slug": "這個網址代稱已經有活動用了。"}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetCampaignActive switches a promotion on or off.
func (s *Store) SetCampaignActive(ctx context.Context, slug string, active bool) error {
	return s.audited(ctx, Event{
		Action: ActionToggleCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"slug": slug}, After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignActive(ctx, db.SetCampaignActiveParams{
				Slug: strings.TrimSpace(slug), IsActive: active,
			})
			if err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// FeatureProduct adds a product to a campaign.
//
// sale_campaign_needs_discount decides whether it may be featured, under a lock
// it takes on the product. Checking here as well would be checking without one
// — a concurrent price change between the check and the write is exactly what
// that guard exists to lose safely.
func (s *Store) FeatureProduct(ctx context.Context, campaign, product string) error {
	return s.audited(ctx, Event{
		Action: ActionFeatureProduct, Table: "sale_campaign_products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"campaign": campaign, "product": product},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.AddCampaignProduct(ctx, db.AddCampaignProductParams{
				Campaign: strings.TrimSpace(campaign), Product: strings.TrimSpace(product),
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

// UnfeatureProduct removes one.
func (s *Store) UnfeatureProduct(ctx context.Context, campaign, product string) error {
	return s.audited(ctx, Event{
		Action: ActionUnfeatureProduct, Table: "sale_campaign_products",
		Before: map[string]any{"campaign": campaign, "product": product},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.RemoveCampaignProduct(ctx, db.RemoveCampaignProductParams{
				Campaign: strings.TrimSpace(campaign), Product: strings.TrimSpace(product),
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
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
