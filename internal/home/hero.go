package home

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// lifestyle is the hero's second tile, in the order the shop means it: the
// first slide an editor scheduled, else the running campaign that ends first,
// else a department over the bundled photograph; nil in a shop with none of
// them.
func (s *Store) lifestyle(ctx context.Context, src *sources, featured *db.RootCategoriesRow) (*hero.Slide, error) {
	rows, err := s.q.HeroSlides(ctx, db.HeroSlidesParams{Locale: string(i18n.FromContext(ctx)), MaxSlides: 1})
	if err != nil {
		return nil, fmt.Errorf("read hero slides: %w", err)
	}
	if len(rows) > 0 {
		r := &rows[0]
		slide := &hero.Slide{
			ID:      r.ID.String(),
			Source:  hero.SourceScheduled,
			Eyebrow: r.Eyebrow,
			Title:   r.Headline,
			Fact:    r.Body,
		}
		// Typed by a person and rendered into an href at the top of the home
		// page; direct SQL bypasses the write gate, so a bad link is a tile
		// with no link rather than a dead one.
		if href, ok := siteurl.Path(r.PrimaryCtaHref); ok {
			slide.CTA = hero.CTA{Label: r.PrimaryCtaLabel, Href: href}
		}
		if digest := r.MediaDigest.String; digest != "" {
			photo := picture.Image{Digest: digest, Width: int(r.ImageWidth)}
			slide.Photo = widget.Photo{URL: photo.URL(), Srcset: photo.Srcset()}
		}
		return slide, nil
	}
	if len(src.camps) > 0 {
		return s.campaignSlide(ctx, &src.camps[0]), nil
	}
	if c := lifestyleOf(src, featured); c != nil {
		return &hero.Slide{
			Source:  hero.SourceDepartment,
			Eyebrow: i18n.Count(ctx, i18n.KeyHomeTileItems, src.held[c.ID], src.held[c.ID]),
			Title:   c.Name,
			Fact:    strings.Join(src.subs[c.ID], " · "),
			CTA:     hero.CTA{Label: c.Name, Href: "/c/" + c.Slug},
		}, nil
	}
	return nil, nil
}

// lifestyleOf is the department the lifestyle tile falls back to: the one the
// bundled photograph shows, where it holds products and is not the featured
// one, or else the largest other department.
func lifestyleOf(src *sources, featured *db.RootCategoriesRow) *db.RootCategoriesRow {
	others := src.others(featured)
	var best *db.RootCategoriesRow
	for _, c := range others {
		if c.Slug == lifestyleDepartment {
			return c
		}
		if best == nil || src.held[c.ID] > src.held[best.ID] {
			best = c
		}
	}
	return best
}

// campaignSlide is a campaign the query lists, which is always running: its
// name, the phrase of its end, how many products it holds and the way to them.
func (s *Store) campaignSlide(ctx context.Context, c *db.ListedCampaignsRow) *hero.Slide {
	slide := &hero.Slide{
		Source:  hero.SourceCampaign,
		Eyebrow: i18n.T(ctx, i18n.KeyHomeTileRunning),
		Title:   c.Title,
		Fact:    i18n.Count(ctx, i18n.KeyHomeTileItems, c.Products, c.Products),
		Ends:    campaign.Ends(campaign.NewEnd(c.EndsAt, s.now())),
		CTA:     hero.CTA{Label: c.Title, Href: "/s/" + c.Slug},
	}
	if c.ImageDigest != "" || c.ImageBundledKey != "" {
		photo := picture.Image{Digest: c.ImageDigest, BundledKey: c.ImageBundledKey, Width: int(c.ImageWidth)}
		slide.Photo = widget.Photo{URL: photo.URL(), Srcset: photo.Srcset(), Alt: c.ImageAlt}
	}
	return slide
}

// Carousel is what the home page's lifestyle tile shows now, one slide or
// none, so the back office lists what shoppers see: cmd/goen hands this store
// to the slides desk as its slides.Storefront.
func (s *Store) Carousel(ctx context.Context) ([]hero.Slide, error) {
	src, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	slide, err := s.lifestyle(ctx, &src, src.featured())
	if err != nil || slide == nil {
		return nil, err
	}
	return []hero.Slide{*slide}, nil
}
