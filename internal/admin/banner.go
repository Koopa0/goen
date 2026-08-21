package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// MaxBanners bounds the back office's list.
const MaxBanners = 20

// MaxBannerRunes bounds the strip's copy: it is ONE row at every width.
const MaxBannerRunes = 60

// BannerForm is what the back office submits.
type BannerForm struct {
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

// Validate refuses what the schema would, and the two things it cannot see.
func (f *BannerForm) Validate(ctx context.Context) map[string]string {
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
		if _, ok := web.SitePath(f.CTAHref); !ok {
			errs["cta"] = i18n.T(ctx, i18n.KeyFormBannerCTAHref)
		}
	}
	if f.Days < 0 || f.Days > MaxHeroDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormRunDays)
	}
	return errs
}

// Banners reads the list.
func (s *Store) Banners(ctx context.Context) ([]pages.AdminBanner, error) {
	rows, err := s.q.ManagedBanners(ctx, MaxBanners)
	if err != nil {
		return nil, fmt.Errorf("read promo banners: %w", err)
	}
	out := make([]pages.AdminBanner, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.AdminBanner{
			ID: r.ID.String(), Message: r.Message, Short: r.MessageShort,
			Code: r.Code, CTALabel: r.CtaLabel, CTAHref: r.CtaHref,
			MessageEn: r.MessageEn, ShortEn: r.MessageShortEn,
			CTALabelEn: r.CtaLabelEn, Active: r.IsActive,
			EndsAt: nullableDate(r.EndsAt),
		})
	}
	return out, nil
}

// CreateBanner adds a promotion, active immediately.
func (s *Store) CreateBanner(ctx context.Context, f *BannerForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := s.audited(ctx, Event{
		Action: ActionCreateBanner, Table: "promo_banners",
		After: map[string]any{"message": f.Message, "cta": f.CTAHref},
	}, func(ctx context.Context, q *db.Queries) error {
		return q.CreateBanner(ctx, db.CreateBannerParams{
			Message: f.Message, MessageShort: f.Short, Code: f.Code,
			CtaLabel: f.CTALabel, CtaHref: f.CTAHref,
			MessageEn: f.MessageEn, MessageShortEn: f.ShortEn,
			CtaLabelEn: f.CTALabelEn, Days: f.Days,
		})
	}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// SetBannerActive toggles a promotion; the dismissal cookie is keyed on the id.
func (s *Store) SetBannerActive(ctx context.Context, id string, active bool) error {
	bannerID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionToggleBanner, Table: "promo_banners", ID: nullableID(bannerID),
		After: map[string]any{"active": active},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.SetBannerActive(ctx, db.SetBannerActiveParams{
			BannerID: bannerID, IsActive: active,
		})
		if execErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
