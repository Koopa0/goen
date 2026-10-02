package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxCampaignDays = 90

const MaxCampaignTitleRunes = 60

type CampaignForm struct {
	Slug    string
	Title   string
	TitleEn string
	Days    int32
	// Tone is stone when the form leaves it out.
	Tone string
}

func (f *CampaignForm) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Title = strings.TrimSpace(f.Title)
	f.TitleEn = strings.TrimSpace(f.TitleEn)

	errs := map[string]string{}
	if !slugFormat.MatchString(f.Slug) {
		errs["slug"] = i18n.T(ctx, i18n.KeyFormSlugFormat)
	}
	if f.Title == "" || utf8.RuneCountInString(f.Title) > MaxCampaignTitleRunes {
		errs["title"] = i18n.T(ctx, i18n.KeyFormCampaignTitle)
	}
	if utf8.RuneCountInString(f.TitleEn) > MaxCampaignTitleRunes {
		errs["title_en"] = i18n.T(ctx, i18n.KeyAdminCampaignTitleEnLength)
	}
	if f.Days < 1 || f.Days > MaxCampaignDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormCampaignDays)
	}
	f.Tone = strings.TrimSpace(f.Tone)
	if f.Tone == "" {
		f.Tone = string(pages.ToneStone)
	}
	if _, ok := pages.ParseTone(f.Tone); !ok {
		errs["tone"] = i18n.T(ctx, i18n.KeyFormToneUnknown)
	}
	return errs
}

func (s *Store) Campaigns(ctx context.Context, after ...string) (admin.CampaignsView, error) {
	scope := "/admin/campaigns"
	cursor := readPageCursor(scope, after)
	rows, err := s.q.AdminCampaigns(ctx, db.AdminCampaignsParams{HasCursor: cursor.Valid, AfterRank: cursor.Rank, AfterAt: cursor.At, AfterID: cursor.ID, RowLimit: PageLimit})
	if err != nil {
		return admin.CampaignsView{}, fmt.Errorf("read campaigns: %w", err)
	}
	rows, bound := pageBound(cursor, scope, rows, PageSize, func(r *db.AdminCampaignsRow) string { return r.PageCursor })
	view := admin.CampaignsView{ListBound: bound}
	for i := range rows {
		c := &rows[i]
		view.Rows = append(view.Rows, admin.CampaignRow{
			Slug: c.Slug, Title: c.Title, Products: c.Products,
			Active: c.IsActive, Running: c.IsRunning,
			StartsAtText: shoptime.Day(c.StartsAt),
			EndsAtText:   shoptime.Minute(c.EndsAt),
		})
	}
	return view, nil
}

const MaxCampaignAltRunes = 200

func (s *Store) CampaignImage(ctx context.Context, slug string) (admin.Header, string, error) {
	row, err := s.q.AdminCampaignImage(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.Header{}, "", ErrNotFound
		}
		return admin.Header{}, "", fmt.Errorf("read campaign image: %w", err)
	}
	return admin.Header{
		Key: row.ImageKey, Alt: row.ImageAlt, AltEn: row.ImageAltEn, Width: row.ImageWidth,
	}, row.Tone, nil
}

func (s *Store) CampaignDetail(ctx context.Context, slug string) (admin.CampaignDetail, error) {
	row, err := s.q.AdminCampaign(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.CampaignDetail{}, ErrNotFound
		}
		return admin.CampaignDetail{}, fmt.Errorf("read campaign: %w", err)
	}
	return admin.CampaignDetail{
		Title:         row.Title,
		StartsAtInput: shoptime.InputMinute(row.StartsAt), EndsAtInput: shoptime.InputMinute(row.EndsAt),
		Active: row.IsActive, Running: row.IsRunning,
	}, nil
}

// SetCampaignWindow moves a campaign's dates, typed on the shop's clock. Create
// bounds a campaign at MaxCampaignDays, so editing may not stretch it past that.
func (s *Store) SetCampaignWindow(ctx context.Context, slug, startsAt, endsAt string) (map[string]string, error) {
	starts, okStart := shoptime.ParseInputMinute(startsAt)
	ends, okEnd := shoptime.ParseInputMinute(endsAt)
	if !okStart || !okEnd || !ends.After(starts) || ends.Sub(starts) > MaxCampaignDays*24*time.Hour {
		return map[string]string{"window": i18n.T(ctx, i18n.KeyFormCampaignWindow)}, nil
	}
	// Filled inside the transaction, which is before the audit row is encoded.
	before := map[string]any{"slug": slug}
	return nil, s.audited(ctx, Event{
		Action: actionSetCampaignWindow, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: before,
		After:  map[string]any{"slug": slug, "starts_at": starts.UTC(), "ends_at": ends.UTC()},
	},
		func(ctx context.Context, q *db.Queries) error {
			prior, err := q.AdminCampaignWindowForUpdate(ctx, strings.TrimSpace(slug))
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("read campaign dates: %w", err)
			}
			before["starts_at"], before["ends_at"] = prior.StartsAt.UTC(), prior.EndsAt.UTC()
			n, err := q.SetCampaignWindow(ctx, db.SetCampaignWindowParams{
				Slug: strings.TrimSpace(slug), StartsAt: starts, EndsAt: ends,
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

const campaignSearchLimit = 10

func (s *Store) SearchCampaignProducts(ctx context.Context, slug, term string) ([]admin.CampaignProduct, error) {
	term = SearchTerm(term)
	if term == "" {
		return nil, nil
	}
	rows, err := s.q.AdminCampaignProductSearch(ctx, db.AdminCampaignProductSearchParams{
		Locale: string(i18n.FromContext(ctx)), Campaign: slug, EscapedTerm: catalog.EscapeLike(term), RowLimit: campaignSearchLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("search campaign products: %w", err)
	}
	out := make([]admin.CampaignProduct, 0, len(rows))
	for i := range rows {
		out = append(out, admin.CampaignProduct{Slug: rows[i].Slug, Name: rows[i].Name})
	}
	return out, nil
}

func (s *Store) SetCampaignTone(ctx context.Context, slug, tone string) error {
	tone = strings.TrimSpace(tone)
	if _, ok := pages.ParseTone(tone); !ok {
		return fmt.Errorf("%w: unknown tone %q", ErrInvalid, tone)
	}
	return s.audited(ctx, Event{
		Action: actionSetCampaignTone, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"campaign": slug}, After: map[string]any{"tone": tone},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignTone(ctx, db.SetCampaignToneParams{Slug: strings.TrimSpace(slug), Tone: tone})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// SetCampaignImage makes a stored upload the campaign's header. The alt text is
// required: sale_campaigns_image_has_alt refuses an image without it.
func (s *Store) SetCampaignImage(ctx context.Context, slug, digest, alt, altEn string) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxCampaignAltRunes ||
		utf8.RuneCountInString(altEn) > MaxCampaignAltRunes {
		return fmt.Errorf("%w: header alt text is required and bounded at %d runes", ErrInvalid, MaxCampaignAltRunes)
	}
	return s.audited(ctx, Event{
		Action: actionSetCampaignImage, Table: "sale_campaigns", ID: uuid.NullUUID{},
		After: map[string]any{"campaign": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignImage(ctx, db.SetCampaignImageParams{
				Slug: strings.TrimSpace(slug), ImageKey: digest, ImageAlt: alt, ImageAltEn: altEn,
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

// ClearCampaignImage removes the header; the media object itself stays.
func (s *Store) ClearCampaignImage(ctx context.Context, slug string) error {
	return s.audited(ctx, Event{
		Action: actionClearCampaignImage, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"campaign": slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.ClearCampaignImage(ctx, strings.TrimSpace(slug))
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

func (s *Store) CreateCampaign(ctx context.Context, f *CampaignForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: actionCreateCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": f.Slug, "title": f.Title, "title_en": f.TitleEn, "days": f.Days, "tone": f.Tone},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCampaign(ctx, db.CreateCampaignParams{
				Slug: f.Slug, Title: f.Title, TitleEn: f.TitleEn, Tone: f.Tone, Days: f.Days,
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
			if err := q.LockCampaignAppendPosition(ctx, strings.TrimSpace(campaign)); err != nil {
				return err
			}
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

func (s *Store) CampaignProducts(ctx context.Context, slug string) ([]admin.CampaignProduct, error) {
	rows, err := s.q.AdminCampaignProducts(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read campaign products: %w", err)
	}
	out := make([]admin.CampaignProduct, 0, len(rows))
	for i := range rows {
		out = append(out, admin.CampaignProduct{
			Slug: rows[i].Slug, Name: rows[i].Name,
		})
	}
	return out, nil
}
