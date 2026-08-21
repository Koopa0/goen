package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxAltRunes bounds the alternative text.
const MaxAltRunes = 200

// AttachImage records an uploaded image. Alt text is required here because
// product_images.alt_text is nullable.
func (s *Store) AttachImage(
	ctx context.Context, slug, digest, alt, altEn string, width, height int32,
) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes {
		return fmt.Errorf("%w: alt text is required and bounded at %d runes", ErrInvalid, MaxAltRunes)
	}
	if utf8.RuneCountInString(altEn) > MaxAltRunes {
		return fmt.Errorf("%w: the English alt text is longer than %d runes", ErrInvalid, MaxAltRunes)
	}
	return s.audited(ctx, Event{
		Action: ActionAttachImage, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.AttachProductImage(ctx, db.AttachProductImageParams{
				Slug: slug, StorageKey: digest, AltText: alt, AltTextEn: altEn,
				Width: width, Height: height,
			}); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// DetachImage removes one from a product; the shared media object is not deleted.
func (s *Store) DetachImage(ctx context.Context, slug, digest string) error {
	return s.audited(ctx, Event{
		Action: ActionDetachImage, Table: "product_images", ID: uuid.NullUUID{},
		Before: map[string]any{"product": slug, "digest": digest},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.DetachProductImage(ctx, db.DetachProductImageParams{
				Slug: slug, StorageKey: digest,
			})
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// ProductImages is what a product shows, for its edit page.
func (s *Store) ProductImages(ctx context.Context, slug string) ([]pages.AdminImage, error) {
	rows, err := s.q.AdminProductImages(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read product images: %w", err)
	}
	out := make([]pages.AdminImage, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.AdminImage{
			Key: r.StorageKey, Alt: r.AltText,
			Width: r.Width.Int32, Height: r.Height.Int32,
		})
	}
	return out, nil
}
