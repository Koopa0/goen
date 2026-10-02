package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

const MaxSlides = 20

// MaxHeadlineRunes bounds the largest text on the site.
const MaxHeadlineRunes = 40

const MaxHeroDays = 365

type HeroForm struct {
	Eyebrow        string
	Headline       string
	Body           string
	PrimaryLabel   string
	PrimaryHref    string
	SecondLabel    string
	SecondHref     string
	ImageKey       string
	ImageAlt       string
	EyebrowEn      string
	HeadlineEn     string
	BodyEn         string
	PrimaryLabelEn string
	SecondLabelEn  string
	ImageAltEn     string
	Days           int32

	// ImageChosen is a file the form carries that is not stored yet. The alt
	// text rule applies to it before it is decoded, so a slide refused for its
	// copy leaves no stored image behind.
	ImageChosen bool
}

func (f *HeroForm) Validate(ctx context.Context) map[string]string {
	f.Headline = strings.TrimSpace(f.Headline)
	f.PrimaryLabel = strings.TrimSpace(f.PrimaryLabel)
	f.SecondLabel = strings.TrimSpace(f.SecondLabel)
	f.ImageAlt = strings.TrimSpace(f.ImageAlt)

	errs := map[string]string{}
	if f.Headline == "" || utf8.RuneCountInString(f.Headline) > MaxHeadlineRunes {
		errs["headline"] = i18n.T(ctx, i18n.KeyFormHeroHeadline)
	}
	if f.PrimaryLabel == "" {
		errs["primary"] = i18n.T(ctx, i18n.KeyFormHeroPrimary)
	}
	if href, ok := web.SitePath(f.PrimaryHref); ok {
		f.PrimaryHref = href
	} else {
		errs["primaryhref"] = i18n.T(ctx, i18n.KeyFormHeroPrimaryHref)
	}

	// Both or neither: hero_slides_secondary_cta_complete refuses half a button.
	switch {
	case f.SecondLabel == "" && f.SecondHref == "":
	case f.SecondLabel == "" || f.SecondHref == "":
		errs["second"] = i18n.T(ctx, i18n.KeyFormHeroSecondPair)
	default:
		if href, ok := web.SitePath(f.SecondHref); ok {
			f.SecondHref = href
		} else {
			errs["second"] = i18n.T(ctx, i18n.KeyFormHeroSecondHref)
		}
	}

	if (f.ImageKey != "" || f.ImageChosen) && f.ImageAlt == "" {
		errs["alt"] = i18n.T(ctx, i18n.KeyFormHeroAlt)
	}
	if f.Days < 0 || f.Days > MaxHeroDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormRunDays)
	}
	return errs
}

func (s *Store) HeroSlides(ctx context.Context) (admin.HeroView, error) {
	rows, err := s.q.AdminHeroSlides(ctx, MaxSlides)
	if err != nil {
		return admin.HeroView{}, fmt.Errorf("read hero slides: %w", err)
	}
	// The storefront's builder, not a copy of its rules: two copies drift.
	live, err := home.NewStore(s.pool).Carousel(ctx)
	if err != nil {
		return admin.HeroView{}, fmt.Errorf("read carousel: %w", err)
	}
	view := admin.HeroView{Carousel: live}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.HeroSlide{
			ID: r.ID.String(), Eyebrow: r.Eyebrow.String, Headline: r.Headline,
			CTALabel: r.PrimaryCtaLabel, CTAHref: r.PrimaryCtaHref,
			ImageKey: r.ImageKey.String, Active: r.IsActive,
			InWindow: r.InWindow, Position: r.Position,
			EndsAt: shoptime.DayIf(r.EndsAt.Time, r.EndsAt.Valid),
		})
	}
	return view, nil
}

// CreateHeroSlide schedules one at the BACK; Promote is what makes one live.
func (s *Store) CreateHeroSlide(ctx context.Context, f *HeroForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateHeroSlide, Table: "hero_slides",
		After: map[string]any{"headline": f.Headline, "cta": f.PrimaryHref},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.LockHeroAppendPosition(ctx); err != nil {
				return err
			}
			return q.CreateHeroSlide(ctx, db.CreateHeroSlideParams{
				Eyebrow: f.Eyebrow, Headline: f.Headline, Body: f.Body,
				PrimaryCtaLabel: f.PrimaryLabel, PrimaryCtaHref: f.PrimaryHref,
				SecondaryCtaLabel: f.SecondLabel, SecondaryCtaHref: f.SecondHref,
				ImageKey: f.ImageKey, ImageAlt: f.ImageAlt,
				EyebrowEn: f.EyebrowEn, HeadlineEn: f.HeadlineEn, BodyEn: f.BodyEn,
				PrimaryCtaLabelEn:   f.PrimaryLabelEn,
				SecondaryCtaLabelEn: f.SecondLabelEn, ImageAltEn: f.ImageAltEn,
				Days: f.Days,
			})
		})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

func (s *Store) SetHeroSlideActive(ctx context.Context, id string, active bool) error {
	slideID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionToggleHeroSlide, Table: "hero_slides",
		ID:    uuid.NullUUID{UUID: slideID, Valid: true},
		After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, setErr := q.SetHeroSlideActive(ctx, db.SetHeroSlideActiveParams{
				ID: slideID, IsActive: active,
			})
			if setErr != nil {
				return fmt.Errorf("%w: %w", ErrRefused, setErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

func (s *Store) PromoteHeroSlide(ctx context.Context, id string) error {
	slideID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionPromoteHeroSlide, Table: "hero_slides",
		ID: uuid.NullUUID{UUID: slideID, Valid: true},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, promoteErr := q.PromoteHeroSlide(ctx, slideID)
			if promoteErr != nil {
				return fmt.Errorf("%w: %w", ErrRefused, promoteErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}
