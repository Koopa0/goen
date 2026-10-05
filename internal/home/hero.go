package home

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// maxSlides: more than three is a queue, not a hero.
const maxSlides = 3

// slides runs in the order the shop means it: slides an editor scheduled,
// campaigns running (soonest-ending first), then departments with a photograph
// to fill what is left.
func (s *Store) slides(ctx context.Context, cats []db.RootCategoriesRow, subs map[uuid.UUID][]string, camps []db.HomeCampaignsRow) ([]pages.HeroSlide, error) {
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

	for i := range camps {
		if len(out) == maxSlides {
			return out, nil
		}
		c := &camps[i]
		slide := pages.HeroSlide{
			Source: pages.SlideCampaign,
			Layout: pages.SlidePhoto,
			Tone:   pages.ResolveTone(c.Tone),
			Title:  c.Title,
			Fact:   s.campaignFact(ctx, c, i18n.KeyHomeCampaignFact),
			CTA:    pages.CTA{Label: i18n.T(ctx, i18n.KeyHeroCampaignCTA), Href: "/s/" + c.Slug},
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

	for i := range cats {
		if len(out) == maxSlides {
			break
		}
		c := &cats[i]
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
			Fact:  strings.Join(subs[c.ID], " · "),
			CTA: pages.CTA{
				Label: fmt.Sprintf(i18n.T(ctx, i18n.KeyHomeDepartmentCTA), c.Name),
				Href:  "/c/" + c.Slug,
			},
		})
	}
	return out, nil
}

func (s *Store) campaignFact(ctx context.Context, c *db.HomeCampaignsRow, withDay i18n.Key) string {
	day := pages.CampaignEndsOn(ctx, c.EndsAt, s.now())
	if day == "" {
		return i18n.Count(ctx, i18n.KeyCampaignProducts, c.Products, strconv.FormatInt(c.Products, 10))
	}
	return i18n.Count(ctx, withDay, c.Products, c.Products, day)
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
