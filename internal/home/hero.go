package home

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
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
	return pages.Hero{
		Eyebrow:    row.Eyebrow,
		Headline:   row.Headline,
		Body:       row.Body,
		PrimaryCTA: pages.CTA{Label: row.PrimaryCtaLabel, Href: row.PrimaryCtaHref},
		SecondaryCTA: pages.CTA{
			Label: row.SecondaryCtaLabel, Href: row.SecondaryCtaHref.String,
		},
		ImageKey:   row.ImageKey.String,
		ImageAlt:   row.ImageAlt,
		ImageWidth: int(row.ImageWidth),
	}, nil
}
