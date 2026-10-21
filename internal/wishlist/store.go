package wishlist

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("wishlist: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// Items is the customer's saved products that are still listed, the most
// recently saved first.
func (s *Store) Items(ctx context.Context, userID string) ([]Item, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	rows, err := s.q.WishlistItems(ctx, db.WishlistItemsParams{
		UserID: id, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read wishlist: %w", err)
	}
	out := make([]Item, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		photo := assets.Image{Digest: r.ImageDigest, BundledKey: r.ImageBundledKey, Width: int(r.ImageWidth)}
		var soleVariant string
		if r.SoleVariantID != uuid.Nil {
			soleVariant = r.SoleVariantID.String()
		}
		out = append(out, Item{
			SoleVariantID: soleVariant,
			ProductTile: pages.ProductTile{
				Slug:         r.Slug,
				Name:         r.Name,
				Summary:      r.Summary.String,
				Brand:        r.Brand,
				PriceCents:   r.MinPriceCents,
				PriceVaries:  r.PriceVaries,
				CompareCents: r.CompareAtPriceCents.Int64,
				OnSale:       campaign.OnSale(r.InCampaign, r.InStock, r.MinPriceCents, r.CompareAtPriceCents.Int64),
				Rating:       r.Rating,
				RatingCount:  r.RatingCount,
				InStock:      r.InStock,
				Colours:      r.Colours,
				ImageURL:     photo.URL(),
				ImageSrcset:  photo.Srcset(),
				ImageAlt:     r.ImageAlt,
				ImageWidth:   r.ImageWidth,
				ImageHeight:  r.ImageHeight,
			},
		})
	}
	return out, nil
}

func (s *Store) Save(ctx context.Context, userID, slug string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.AddWishlistItem(ctx, db.AddWishlistItemParams{
		UserID: id, Slug: slug,
	}); err != nil {
		return fmt.Errorf("save to wishlist: %w", err)
	}
	return nil
}

func (s *Store) Remove(ctx context.Context, userID, slug string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}
	if err := s.q.RemoveWishlistItem(ctx, db.RemoveWishlistItemParams{
		UserID: id, Slug: slug,
	}); err != nil {
		return fmt.Errorf("remove from wishlist: %w", err)
	}
	return nil
}
