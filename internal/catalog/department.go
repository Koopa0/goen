package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

// DepartmentHead reads what a department page says under its band. Only the department's front
// has a head: a page of filtered results, or a later page, is not the front and gets an empty one.
// The campaign notice is read for every front; the editorial slot (the comparison when the
// department compares its products, else the colour story) only when the front has the
// products to carry it, see ListingView.HasEditorial.
func (s *Store) DepartmentHead(ctx context.Context, v ListingView, offers map[uuid.UUID]bool) (*DepartmentHead, error) {
	head := &DepartmentHead{}
	if !v.IsFront() {
		return head, nil
	}
	var err error
	if head.Notice, err = s.departmentNotice(ctx, v.Slug); err != nil {
		return nil, err
	}
	if !v.HasEditorial() {
		return head, nil
	}
	if v.Theme.Comparable {
		if head.Preview, err = s.comparePreview(ctx, v.Slug, offers); err != nil {
			return nil, err
		}
	}
	if head.Preview == nil {
		if head.Story, err = s.colourStory(ctx, v.Slug); err != nil {
			return nil, err
		}
	}
	return head, nil
}

func (s *Store) departmentNotice(ctx context.Context, slug string) (*DepartmentNotice, error) {
	c, err := s.q.DepartmentCampaign(ctx, db.DepartmentCampaignParams{Slug: slug, Locale: string(i18n.FromContext(ctx))})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read campaign of %q: %w", slug, err)
	}
	return &DepartmentNotice{Title: c.Title, Href: "/s/" + c.Slug, End: campaign.NewEnd(c.EndsAt, s.now())}, nil
}

// comparePreview tries the categories holding the most comparable products, each with its
// three newest, and takes the first whose products share enough specifications.
func (s *Store) comparePreview(ctx context.Context, slug string, offers map[uuid.UUID]bool) (*ComparePreview, error) {
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
		if preview, ok := NewComparePreview(view.Products[0].Category, view); ok {
			return &preview, nil
		}
	}
	return nil, nil
}

func (s *Store) colourStory(ctx context.Context, slug string) (*ColourStory, error) {
	rows, err := s.q.DepartmentColourStory(ctx, db.DepartmentColourStoryParams{Slug: slug, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return nil, fmt.Errorf("read colour story of %q: %w", slug, err)
	}
	if len(rows) < StoryColours {
		return nil, nil
	}
	rows = rows[:StoryColours]
	first := &rows[0]
	story := &ColourStory{
		Slug: first.Slug, Name: first.Name, Summary: first.Summary, Brand: first.Brand,
		Price: tile.Tile{
			PriceCents: first.PriceCents, PriceVaries: first.PriceVaries,
			CompareCents: first.CompareAtPriceCents.Int64, InStock: true,
			OnSale: campaign.OnSale(first.InCampaign, true, first.PriceCents, first.CompareAtPriceCents.Int64),
		},
	}
	for i := range rows {
		r := &rows[i]
		photo := picture.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		story.Plates = append(story.Plates, ColourPlate{
			Colour: r.Colour, Swatch: r.Swatch,
			Image: widget.Photo{
				URL:    photo.URL(),
				Srcset: photo.Srcset(),
				Alt:    r.ImageAlt,
			},
		})
	}
	return story, nil
}

// withTileFacts gives each tile what its card says beyond the listing's row:
// the stated warranty, the variant a quick add puts in the cart, the first
// three specification values, and each colour's name and photograph.
func (s *Store) withTileFacts(ctx context.Context, tiles []tile.Tile) error {
	if len(tiles) == 0 {
		return nil
	}
	slugs := make([]string, len(tiles))
	for i := range tiles {
		slugs[i] = tiles[i].Slug
	}
	rows, err := s.q.ListingTileFacts(ctx, db.ListingTileFactsParams{Slugs: slugs, Locale: string(i18n.FromContext(ctx))})
	if err != nil {
		return fmt.Errorf("read listing tile facts: %w", err)
	}
	bySlug := make(map[string]*db.ListingTileFactsRow, len(rows))
	for i := range rows {
		bySlug[rows[i].Slug] = &rows[i]
	}
	for i := range tiles {
		r, ok := bySlug[tiles[i].Slug]
		if !ok {
			continue
		}
		tiles[i].WarrantyMonths = r.WarrantyMonths
		tiles[i].QuickAdd = r.QuickAdd
		tiles[i].Highlights = r.Highlights
		tiles[i].ColourNames = r.ColourNames
		tiles[i].ColourPhotos = tile.ColourPhotos(r.ColourDigests, r.ColourBundledKeys, r.ColourWidths)
	}
	return nil
}
