package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// DepartmentHead reads what a department page says under its band. The campaign notice and the
// editorial slot are read only when slots is true, which is the unfiltered first page: a page
// of filtered results is not the department's front. compared is whether the department
// puts its products side by side.
func (s *Store) DepartmentHead(ctx context.Context, slug string, compared, slots bool, offers map[uuid.UUID]bool) (*pages.DepartmentHead, error) {
	head := &pages.DepartmentHead{}
	if !slots {
		return head, nil
	}
	var err error
	if head.Notice, err = s.departmentNotice(ctx, slug); err != nil {
		return nil, err
	}
	if compared {
		if head.Preview, err = s.comparePreview(ctx, slug, offers); err != nil {
			return nil, err
		}
	}
	if head.Preview == nil {
		if head.Story, err = s.colourStory(ctx, slug); err != nil {
			return nil, err
		}
	}
	return head, nil
}

func (s *Store) departmentNotice(ctx context.Context, slug string) (*pages.DepartmentNotice, error) {
	c, err := s.q.DepartmentCampaign(ctx, db.DepartmentCampaignParams{Slug: slug, Locale: string(i18n.FromContext(ctx))})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read campaign of %q: %w", slug, err)
	}
	return &pages.DepartmentNotice{Title: c.Title, Href: "/s/" + c.Slug, End: pages.NewCampaignEnd(c.EndsAt, s.now())}, nil
}

// comparePreview tries the categories holding the most comparable products, each with its
// three newest, and takes the first whose products share enough specifications.
func (s *Store) comparePreview(ctx context.Context, slug string, offers map[uuid.UUID]bool) (*pages.ComparePreview, error) {
	rows, err := s.q.DepartmentCompareCandidates(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read comparison candidates of %q: %w", slug, err)
	}
	var order []uuid.UUID
	slugs := make(map[uuid.UUID][]string)
	for _, r := range rows {
		if !offers[r.CategoryID] {
			continue
		}
		if _, seen := slugs[r.CategoryID]; !seen {
			order = append(order, r.CategoryID)
		}
		slugs[r.CategoryID] = append(slugs[r.CategoryID], r.Slug)
	}
	const tried = 3
	for _, id := range order[:min(len(order), tried)] {
		view, err := s.Compare(ctx, slugs[id])
		if err != nil {
			return nil, err
		}
		if len(view.Products) == 0 {
			continue
		}
		if preview, ok := pages.NewComparePreview(view.Products[0].Category, view); ok {
			return &preview, nil
		}
	}
	return nil, nil
}

func (s *Store) colourStory(ctx context.Context, slug string) (*pages.ColourStory, error) {
	rows, err := s.q.DepartmentColourStory(ctx, db.DepartmentColourStoryParams{Slug: slug, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return nil, fmt.Errorf("read colour story of %q: %w", slug, err)
	}
	if len(rows) < pages.StoryColours {
		return nil, nil
	}
	rows = rows[:pages.StoryColours]
	first := &rows[0]
	story := &pages.ColourStory{
		Slug: first.Slug, Name: first.Name, Summary: first.Summary, Brand: first.Brand,
		Price: pages.ProductTile{
			PriceCents: first.PriceCents, PriceVaries: first.PriceVaries,
			CompareCents: first.CompareAtPriceCents.Int64, InCampaign: first.InCampaign, InStock: true,
		},
	}
	for i := range rows {
		r := &rows[i]
		story.Plates = append(story.Plates, pages.ColourPlate{
			Colour: r.Colour, Swatch: r.Swatch,
			Image: pages.Photo{
				URL:    assets.ProductImageURL(r.ImageKey),
				Srcset: assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
				Alt:    r.ImageAlt,
			},
		})
	}
	return story, nil
}

// withHighlights gives each comparable tile the first specifications it lists.
func (s *Store) withHighlights(ctx context.Context, tiles []pages.ProductTile) error {
	var slugs []string
	for i := range tiles {
		if tiles[i].Comparable {
			slugs = append(slugs, tiles[i].Slug)
		}
	}
	if len(slugs) == 0 {
		return nil
	}
	rows, err := s.q.ListingHighlights(ctx, db.ListingHighlightsParams{Slugs: slugs, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return fmt.Errorf("read listing highlights: %w", err)
	}
	bySlug := make(map[string][]string, len(slugs))
	for _, r := range rows {
		bySlug[r.Slug] = append(bySlug[r.Slug], r.Value)
	}
	for i := range tiles {
		tiles[i].Highlights = bySlug[tiles[i].Slug]
	}
	return nil
}
