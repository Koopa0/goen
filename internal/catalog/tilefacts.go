package catalog

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
)

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
