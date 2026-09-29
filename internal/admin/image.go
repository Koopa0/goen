package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
		Action: actionAttachImage, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			// The same lock, taken before the next position is computed: MoveImage
			// renumbers under it, and an attach that read positions mid-reorder
			// would collide on the unique index. A missing product falls through
			// to an insert that matches nothing.
			if _, err := q.LockProductForImages(ctx, slug); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
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
		Action: actionDetachImage, Table: "product_images", ID: uuid.NullUUID{},
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

// ImageMove is where an image goes in a product's order. The first image is the
// cover everywhere the shop shows one.
type ImageMove string

// The moves the image list offers.
const (
	MoveToCover ImageMove = "cover"
	MoveUp      ImageMove = "up"
	MoveDown    ImageMove = "down"
)

// MoveImage reorders a product's images and renumbers them 0..n-1. A digest the
// product no longer has, or a move that would change nothing, is ErrInvalid: the
// page the staff member acted on is out of date.
func (s *Store) MoveImage(ctx context.Context, slug, digest string, move ImageMove) error {
	order := []string{}
	return s.audited(ctx, Event{
		Action: actionMoveImage, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": digest, "move": string(move), "order": &order},
	},
		func(ctx context.Context, q *db.Queries) error {
			if _, err := q.LockProductForImages(ctx, slug); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			rows, err := q.ProductImageOrder(ctx, slug)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			at := -1
			for i := range rows {
				if rows[i].StorageKey == digest {
					at = i
				}
			}
			placed, ok := placeImage(rows, at, move)
			if !ok {
				return fmt.Errorf("%w: image %q cannot move %s", ErrInvalid, digest, move)
			}
			rows = placed
			ids := make([]uuid.UUID, len(rows))
			for i := range rows {
				ids[i] = rows[i].ID
				order = append(order, rows[i].StorageKey)
			}
			if err := q.ParkProductImages(ctx, slug); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if err := q.SetProductImageOrder(ctx, ids); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// placeImage returns items with the one at index at moved as asked, and false
// when it is not there or the move would change nothing.
func placeImage[T any](items []T, at int, move ImageMove) ([]T, bool) {
	to := at
	switch move {
	case MoveToCover:
		to = 0
	case MoveUp:
		to = at - 1
	case MoveDown:
		to = at + 1
	}
	if at < 0 || at >= len(items) || to < 0 || to >= len(items) || to == at {
		return nil, false
	}
	moved := items[at]
	out := slices.Insert(slices.Delete(slices.Clone(items), at, at+1), to, moved)
	return out, true
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
