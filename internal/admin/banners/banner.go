package banners

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/web/siteurl"
)

const MaxBanners = 20

// MaxBannerRunes bounds the strip's copy: it is ONE row at every width.
const MaxBannerRunes = 60

// MaxDays bounds how long a strip runs; slides.MaxDays is the same bound for a
// carousel slide, and KeyFormRunDays states it to staff.
const MaxDays = 365

type Form struct {
	Message    string
	Short      string
	Code       string
	CTALabel   string
	CTAHref    string
	MessageEn  string
	ShortEn    string
	CTALabelEn string
	Days       int32
}

func (f *Form) Validate(ctx context.Context) map[string]string {
	f.Message = strings.TrimSpace(f.Message)
	f.Short = strings.TrimSpace(f.Short)
	f.Code = strings.TrimSpace(f.Code)
	f.CTALabel = strings.TrimSpace(f.CTALabel)
	f.CTAHref = strings.TrimSpace(f.CTAHref)
	f.MessageEn = strings.TrimSpace(f.MessageEn)
	f.ShortEn = strings.TrimSpace(f.ShortEn)
	f.CTALabelEn = strings.TrimSpace(f.CTALabelEn)

	errs := map[string]string{}
	if f.Message == "" || utf8.RuneCountInString(f.Message) > MaxBannerRunes {
		errs["message"] = i18n.T(ctx, i18n.KeyFormBannerMessage)
	}
	for field, value := range map[string]string{
		"short":      f.Short,
		"message_en": f.MessageEn,
		"short_en":   f.ShortEn,
	} {
		if utf8.RuneCountInString(value) > MaxBannerRunes {
			errs[field] = i18n.T(ctx, i18n.KeyFormBannerFieldLong)
		}
	}

	if (f.CTALabel == "") != (f.CTAHref == "") {
		errs["cta"] = i18n.T(ctx, i18n.KeyFormBannerCTAPair)
	}
	if f.CTAHref != "" {
		if _, ok := siteurl.Path(f.CTAHref); !ok {
			errs["cta"] = i18n.T(ctx, i18n.KeyFormBannerCTAHref)
		}
	}
	if f.Days < 0 || f.Days > MaxDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormRunDays)
	}
	return errs
}

const bannerScope = "/admin/banners#banner-history"

type bannerPosition struct {
	Active bool
	At     time.Time
	ID     uuid.UUID
}

func (s *Store) Banners(ctx context.Context, after ...string) (View, error) {
	from, resumed := keyset.Resume(bannerScope, after, func(p bannerPosition) bool { return p.ID != uuid.Nil && !p.At.IsZero() })
	rows, err := s.q.ManagedBanners(ctx, db.ManagedBannersParams{
		HasCursor: resumed, AfterActive: from.Active, AfterAt: from.At, AfterID: from.ID, RowLimit: MaxBanners + 1,
	})
	if err != nil {
		return View{}, fmt.Errorf("read promo banners: %w", err)
	}
	rows, bound := keyset.PageBound(bannerScope, resumed, rows, MaxBanners, func(r *db.ManagedBannersRow) string { return r.PageCursor })
	out := View{Rows: make([]Banner, 0, len(rows)), Bound: bound}
	for i := range rows {
		r := &rows[i]
		out.Rows = append(out.Rows, Banner{
			ID: r.ID.String(), Message: r.Message, Short: r.MessageShort,
			Code: r.Code, CTALabel: r.CtaLabel, CTAHref: r.CtaHref,
			MessageEn: r.MessageEn, ShortEn: r.MessageShortEn,
			CTALabelEn: r.CtaLabelEn, Active: r.IsActive,
			EndsAt: shoptime.DayIf(r.EndsAt.Time, r.EndsAt.Valid),
		})
	}
	return out, nil
}

func (s *Store) CreateBanner(ctx context.Context, f *Form) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateBanner, Table: "promo_banners",
		After: audit.State{audit.FieldMessage: f.Message, audit.FieldCTA: f.CTAHref},
	}, func(ctx context.Context, q *db.Queries) error {
		return q.CreateBanner(ctx, db.CreateBannerParams{
			Message: f.Message, MessageShort: f.Short, Code: f.Code,
			CtaLabel: f.CTALabel, CtaHref: f.CTAHref,
			MessageEn: f.MessageEn, MessageShortEn: f.ShortEn,
			CtaLabelEn: f.CTALabelEn, Days: f.Days,
		})
	}); err != nil {
		return nil, db.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

// SetBannerActive toggles a promotion; the dismissal cookie is keyed on the id.
func (s *Store) SetBannerActive(ctx context.Context, id string, active bool) error {
	bannerID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionToggleBanner, Table: "promo_banners", ID: audit.EntityID(bannerID),
		After: audit.State{audit.FieldActive: active},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.SetBannerActive(ctx, db.SetBannerActiveParams{
			BannerID: bannerID, IsActive: active,
		})
		if execErr != nil {
			return db.WrapRefusal(execErr, ErrRefused)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
