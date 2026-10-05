// Package campaigns is the back office's sale campaigns: creating one, its
// dates, tone and header image, and which products it features.
package campaigns

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound = errors.New("campaigns: not found")
	ErrInvalid  = errors.New("campaigns: invalid input")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("campaigns: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("campaigns: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// position is a reader's place in the list. The query builds it as PageCursor,
// so its fields are the ordering values and nothing else.
type position struct {
	Rank bool
	ID   uuid.UUID
	At   time.Time
}

const MaxDays = 90

const MaxTitleRunes = 60

type Form struct {
	Slug    string
	Title   string
	TitleEn string
	Days    int32
	// Tone is stone when the form leaves it out.
	Tone string
}

func (f *Form) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Title = strings.TrimSpace(f.Title)
	f.TitleEn = strings.TrimSpace(f.TitleEn)

	errs := map[string]string{}
	if !web.ValidSlug(f.Slug) {
		errs["slug"] = i18n.T(ctx, i18n.KeyFormSlugFormat)
	}
	if f.Title == "" || utf8.RuneCountInString(f.Title) > MaxTitleRunes {
		errs["title"] = i18n.T(ctx, i18n.KeyFormCampaignTitle)
	}
	if utf8.RuneCountInString(f.TitleEn) > MaxTitleRunes {
		errs["title_en"] = i18n.T(ctx, i18n.KeyAdminCampaignTitleEnLength)
	}
	if f.Days < 1 || f.Days > MaxDays {
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

func (s *Store) List(ctx context.Context, after ...string) (admin.CampaignsView, error) {
	const scope = "/admin/campaigns"
	from, resumed := web.ResumeKeyset(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminCampaigns(ctx, db.AdminCampaignsParams{HasCursor: resumed, AfterRank: from.Rank, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.CampaignsView{}, fmt.Errorf("read campaigns: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminCampaignsRow) string { return r.PageCursor })
	view := admin.CampaignsView{Bound: bound}
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

const MaxAltRunes = 200

func (s *Store) Image(ctx context.Context, slug string) (admin.Header, string, error) {
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

func (s *Store) Detail(ctx context.Context, slug string) (admin.CampaignDetail, error) {
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

// SetWindow moves a campaign's dates, typed on the shop's clock. Create
// bounds a campaign at MaxDays, so editing may not stretch it past that.
func (s *Store) SetWindow(ctx context.Context, slug, startsAt, endsAt string) (map[string]string, error) {
	starts, okStart := shoptime.ParseInputMinute(startsAt)
	ends, okEnd := shoptime.ParseInputMinute(endsAt)
	if !okStart || !okEnd || !ends.After(starts) || ends.Sub(starts) > MaxDays*24*time.Hour {
		return map[string]string{"window": i18n.T(ctx, i18n.KeyFormCampaignWindow)}, nil
	}
	// Filled inside the transaction, which is before the audit row is encoded.
	before := map[string]any{"slug": slug}
	return nil, audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetCampaignWindow, Table: "sale_campaigns", ID: uuid.NullUUID{},
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
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

const searchLimit = 10

func (s *Store) SearchProducts(ctx context.Context, slug, term string) ([]admin.CampaignProduct, error) {
	term = web.SearchTerm(term)
	if term == "" {
		return nil, nil
	}
	rows, err := s.q.AdminCampaignProductSearch(ctx, db.AdminCampaignProductSearchParams{
		Locale: string(i18n.FromContext(ctx)), Campaign: slug, EscapedTerm: catalog.EscapeLike(term), RowLimit: searchLimit,
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

func (s *Store) SetTone(ctx context.Context, slug, tone string) error {
	tone = strings.TrimSpace(tone)
	if _, ok := pages.ParseTone(tone); !ok {
		return fmt.Errorf("%w: unknown tone %q", ErrInvalid, tone)
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetCampaignTone, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"campaign": slug}, After: map[string]any{"tone": tone},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignTone(ctx, db.SetCampaignToneParams{Slug: strings.TrimSpace(slug), Tone: tone})
			if err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// SetImage makes a stored upload the campaign's header. The alt text is
// required: sale_campaigns_image_has_alt refuses an image without it.
func (s *Store) SetImage(ctx context.Context, slug, digest, alt, altEn string) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes ||
		utf8.RuneCountInString(altEn) > MaxAltRunes {
		return fmt.Errorf("%w: header alt text is required and bounded at %d runes", ErrInvalid, MaxAltRunes)
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetCampaignImage, Table: "sale_campaigns", ID: uuid.NullUUID{},
		After: map[string]any{"campaign": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignImage(ctx, db.SetCampaignImageParams{
				Slug: strings.TrimSpace(slug), ImageKey: digest, ImageAlt: alt, ImageAltEn: altEn,
			})
			if err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// ClearImage removes the header; the media object itself stays.
func (s *Store) ClearImage(ctx context.Context, slug string) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionClearCampaignImage, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"campaign": slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.ClearCampaignImage(ctx, strings.TrimSpace(slug))
			if err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

func (s *Store) Create(ctx context.Context, f *Form) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": f.Slug, "title": f.Title, "title_en": f.TitleEn, "days": f.Days, "tone": f.Tone},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCampaign(ctx, db.CreateCampaignParams{
				Slug: f.Slug, Title: f.Title, TitleEn: f.TitleEn, Tone: f.Tone, Days: f.Days,
			})
		})
	if err != nil {
		if pgerr.IsConstraint(err, "sale_campaigns_slug_key") {
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenCampaign)}, nil
		}
		return nil, pgerr.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

func (s *Store) SetActive(ctx context.Context, slug string, active bool) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionToggleCampaign, Table: "sale_campaigns", ID: uuid.NullUUID{},
		Before: map[string]any{"slug": slug}, After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCampaignActive(ctx, db.SetCampaignActiveParams{
				Slug: strings.TrimSpace(slug), IsActive: active,
			})
			if err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// FeatureProduct adds a product; sale_campaign_needs_discount decides eligibility.
func (s *Store) FeatureProduct(ctx context.Context, campaign, product string) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionFeatureProduct, Table: "sale_campaign_products", ID: uuid.NullUUID{},
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
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

func (s *Store) UnfeatureProduct(ctx context.Context, campaign, product string) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionUnfeatureProduct, Table: "sale_campaign_products",
		Before: map[string]any{"campaign": campaign, "product": product},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.RemoveCampaignProduct(ctx, db.RemoveCampaignProductParams{
				Campaign: strings.TrimSpace(campaign), Product: strings.TrimSpace(product),
			}); err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			return nil
		})
}

func (s *Store) Products(ctx context.Context, slug string) ([]admin.CampaignProduct, error) {
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
