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

// MaxSlides bounds the back office's list.
//
// Small, because hero_slides is a QUEUE and not a carousel: one shows, the rest
// are scheduled. A shop with more than this many queued has lost track of them.
const MaxSlides = 20

// MaxHeadlineRunes bounds the largest text on the site.
const MaxHeadlineRunes = 40

// MaxHeroDays bounds how long a slide may be scheduled for.
const MaxHeroDays = 365

// HeroForm is what the back office submits.
type HeroForm struct {
	Eyebrow      string
	Headline     string
	Body         string
	PrimaryLabel string
	PrimaryHref  string
	SecondLabel  string
	SecondHref   string
	ImageKey     string
	ImageAlt     string
	// The English hero, each field optional. The HREFs have no English twin: a
	// link goes to one page.
	EyebrowEn      string
	HeadlineEn     string
	BodyEn         string
	PrimaryLabelEn string
	SecondLabelEn  string
	ImageAltEn     string
	Days           int32
}

// Validate refuses what the schema would, and what the schema cannot see.
//
// The CTA hrefs are the reason this is more than a length check: they are
// written by a person and rendered into an href, so an absolute URL here would
// put an off-site link in the largest button on the storefront — and
// "javascript:" would put script there. web.SitePath is the one owner of that
// rule, the same one guarding the wishlist and the language switch.
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

	// Both or neither: hero_slides_secondary_cta_complete refuses half a
	// button, and refusing it here says which half is missing.
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

	// An image without alt text is refused by the schema. Asking here means the
	// editor is told which field, rather than shown a constraint name.
	if f.ImageKey != "" && f.ImageAlt == "" {
		errs["alt"] = i18n.T(ctx, i18n.KeyFormHeroAlt)
	}
	if f.Days < 0 || f.Days > MaxHeroDays {
		errs["days"] = i18n.T(ctx, i18n.KeyFormRunDays)
	}
	return errs
}

// HeroSlides reads the queue.
func (s *Store) HeroSlides(ctx context.Context) (pages.AdminHeroView, error) {
	rows, err := s.q.AdminHeroSlides(ctx, MaxSlides)
	if err != nil {
		return pages.AdminHeroView{}, fmt.Errorf("read hero slides: %w", err)
	}
	view := pages.AdminHeroView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminHeroSlide{
			ID: r.ID.String(), Eyebrow: r.Eyebrow.String, Headline: r.Headline,
			CTALabel: r.PrimaryCtaLabel, CTAHref: r.PrimaryCtaHref,
			ImageKey: r.ImageKey.String, Active: r.IsActive,
			InWindow: r.InWindow, Position: r.Position,
			EndsAt: nullableDate(r.EndsAt),
		})
	}
	return view, nil
}

// CreateHeroSlide queues one.
//
// It goes to the BACK of the queue, not the front. Creating a slide and having
// it replace the live home page immediately is a decision an editor should make
// on purpose, which is what Promote is for.
func (s *Store) CreateHeroSlide(ctx context.Context, f *HeroForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: ActionCreateHeroSlide, Table: "hero_slides",
		After: map[string]any{"headline": f.Headline, "cta": f.PrimaryHref},
	},
		func(ctx context.Context, q *db.Queries) error {
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
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetHeroSlideActive switches one on or off.
func (s *Store) SetHeroSlideActive(ctx context.Context, id string, active bool) error {
	slideID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionToggleHeroSlide, Table: "hero_slides",
		ID:    uuid.NullUUID{UUID: slideID, Valid: true},
		After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, setErr := q.SetHeroSlideActive(ctx, db.SetHeroSlideActiveParams{
				ID: slideID, IsActive: active,
			})
			if setErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, setErr.Error())
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// PromoteHeroSlide makes one the slide that shows.
func (s *Store) PromoteHeroSlide(ctx context.Context, id string) error {
	slideID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionPromoteHeroSlide, Table: "hero_slides",
		ID: uuid.NullUUID{UUID: slideID, Valid: true},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, promoteErr := q.PromoteHeroSlide(ctx, slideID)
			if promoteErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, promoteErr.Error())
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}
