package home

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Hero reads the slide the home page should show, or the built-in one. A missing
// row is not an error; a read failure is.
func (s *Store) Hero(ctx context.Context) (pages.Hero, error) {
	row, err := s.q.CurrentHeroSlide(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.DefaultHero(ctx), nil
		}
		return pages.Hero{}, fmt.Errorf("read hero slide: %w", err)
	}

	// Typed by a person and rendered into an href at the top of the home page.
	primaryHref, primaryOK := web.SitePath(row.PrimaryCtaHref)
	primary := pages.CTA{Label: row.PrimaryCtaLabel, Href: primaryHref}
	secondary := pages.CTA{}
	if !primaryOK {
		// A primary CTA is required as a pair, and direct SQL bypasses the write
		// gate: keep the slide's copy rather than render a dead primary button.
		defaults := pages.DefaultHero(ctx)
		primary, secondary = defaults.PrimaryCTA, defaults.SecondaryCTA
	} else {
		secondaryHref, secondaryOK := web.SitePath(row.SecondaryCtaHref.String)
		if secondaryOK {
			secondary = pages.CTA{Label: row.SecondaryCtaLabel, Href: secondaryHref}
		}
	}

	return pages.Hero{
		Eyebrow:      row.Eyebrow,
		Headline:     row.Headline,
		Body:         row.Body,
		PrimaryCTA:   primary,
		SecondaryCTA: secondary,
		ImageKey:     row.ImageKey.String,
		ImageAlt:     row.ImageAlt,
		ImageWidth:   int(row.ImageWidth),
	}, nil
}
