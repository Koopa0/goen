package home

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// maxSlides: more than three is a queue, not a hero.
const maxSlides = 3

// slides runs in the order the shop means it: slides an editor scheduled,
// campaigns running (soonest-ending first), then departments with a photograph
// to fill what is left.
func (s *Store) slides(ctx context.Context, src carouselSources) ([]pages.HeroSlide, error) {
	locale := i18n.FromContext(ctx)
	rows, err := s.q.HeroSlides(ctx, db.HeroSlidesParams{Locale: string(locale), MaxSlides: maxSlides})
	if err != nil {
		return nil, fmt.Errorf("read hero slides: %w", err)
	}

	out := make([]pages.HeroSlide, 0, maxSlides)
	for i := range rows {
		r := &rows[i]
		slide := pages.HeroSlide{
			ID:     r.ID.String(),
			Source: pages.SlideScheduled,
			Layout: pages.SlidePhoto,
			Tone:   pages.ToneStone,
			Title:  r.Headline,
			Fact:   r.Body,
		}
		// Typed by a person and rendered into an href at the top of the home
		// page; direct SQL bypasses the write gate, so a bad link is a slide
		// with no button rather than a dead one.
		if href, ok := web.SitePath(r.PrimaryCtaHref); ok {
			slide.CTA = pages.CTA{Label: r.PrimaryCtaLabel, Href: href}
			slide.Tone = src.toneOf(href)
		}
		if key := r.ImageKey.String; key != "" {
			slide.Photo = pages.Photo{
				URL:    assets.MediaURL(key),
				Srcset: assets.ProductImageSrcsetAt(key, int(r.ImageWidth)),
				Alt:    r.ImageAlt,
			}
			slide.PhotoWidth, slide.PhotoHeight = int(r.ImageWidth), int(r.ImageHeight)
		}
		out = append(out, slide)
	}

	for i := range src.camps {
		if len(out) == maxSlides {
			return out, nil
		}
		c := &src.camps[i]
		slide := pages.HeroSlide{
			Source: pages.SlideCampaign,
			Layout: pages.SlidePhoto,
			Tone:   pages.ResolveTone(c.Tone),
			Title:  c.Title,
			Stats:  s.campaignStats(ctx, c),
			CTA:    pages.CTA{Label: i18n.T(ctx, i18n.KeyHeroCampaignCTA), Href: "/s/" + c.Slug},
		}
		if period, ok := components.DayPeriod(ctx, c.Title, c.StartsAt, c.EndsAt, s.now()); ok {
			slide.Period = &period
		}
		if c.ImageKey != "" {
			slide.Photo = pages.Photo{
				URL:    assets.ProductImageURL(c.ImageKey),
				Srcset: assets.ProductImageSrcsetAt(c.ImageKey, int(c.ImageWidth)),
				Alt:    c.ImageAlt,
			}
			slide.PhotoWidth, slide.PhotoHeight = 1600, 600
		}
		out = append(out, slide)
	}

	for i := range src.cats {
		if len(out) == maxSlides {
			break
		}
		c := &src.cats[i]
		photo := departmentPhoto(c)
		if !photo.Shown() {
			continue
		}
		out = append(out, pages.HeroSlide{
			Source:     pages.SlideDepartment,
			Layout:     pages.SlideSplit,
			Tone:       pages.ResolveTone(c.Tone),
			Photo:      photo,
			PhotoWidth: 1600, PhotoHeight: 1200,
			Title: c.Name,
			Stats: []components.Stat{
				{Label: i18n.T(ctx, i18n.KeySlideItems), Value: components.StatCount(src.held[c.ID], i18n.T(ctx, i18n.KeyFactUnitItems))},
				{Label: i18n.T(ctx, i18n.KeySlideCategories), Value: components.StatCount(int64(len(src.subs[c.ID])), i18n.T(ctx, i18n.KeyFactUnitCategories))},
			},
			CTA: pages.CTA{Label: i18n.T(ctx, i18n.KeyHeroCampaignCTA), Href: "/c/" + c.Slug},
		})
	}
	return out, nil
}

// campaignStats is the fact line of a campaign the query lists, which is always running.
func (s *Store) campaignStats(ctx context.Context, c *db.ListedCampaignsRow) []components.Stat {
	return s.campaignSchedule(ctx, c).Facts
}

func (s *Store) campaignSchedule(ctx context.Context, c *db.ListedCampaignsRow) pages.CampaignSchedule {
	return pages.NewCampaignSchedule(ctx, c.Title, c.Products, c.StartsAt, c.EndsAt, s.now())
}

func departmentPhoto(c *db.RootCategoriesRow) pages.Photo {
	url := assets.ProductImageURL(c.ImageKey)
	if url == "" {
		return pages.Photo{}
	}
	return pages.Photo{
		URL:    url,
		Srcset: assets.ProductImageSrcsetAt(c.ImageKey, int(c.ImageWidth)),
		Alt:    c.ImageAlt,
	}
}

// toneOf is the tone of the campaign or department a link goes to; any other
// link, and a link to nothing the shop has, is stone.
func (src carouselSources) toneOf(href string) pages.Tone {
	for i := range src.camps {
		if href == "/s/"+src.camps[i].Slug {
			return pages.ResolveTone(src.camps[i].Tone)
		}
	}
	for i := range src.cats {
		if href == "/c/"+src.cats[i].Slug {
			return pages.ResolveTone(src.cats[i].Tone)
		}
	}
	return pages.ToneStone
}
