package products

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxAltRunes = 200

var ErrNotThisProductsOption = errors.New("products: that option value is not one of this product's")

// AttachImage records an uploaded image, showing optionValue when that is an
// option value id and the product whichever value is chosen when it is empty.
// Alt text is required here because product_images.alt_text is nullable.
func (s *Store) AttachImage(
	ctx context.Context, slug, digest, alt, altEn, optionValue string, width, height int32,
) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes {
		return fmt.Errorf("%w: alt text is required and bounded at %d runes", ErrInvalid, MaxAltRunes)
	}
	if utf8.RuneCountInString(altEn) > MaxAltRunes {
		return fmt.Errorf("%w: the English alt text is longer than %d runes", ErrInvalid, MaxAltRunes)
	}
	shows, err := optionValueRef(optionValue)
	if err != nil {
		return err
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAttachImage, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": digest, "alt": alt, "option_value": shows},
	},
		func(ctx context.Context, q *db.Queries) error {
			// The same lock, taken before the next position is computed: MoveImage
			// renumbers under it, and an attach that read positions mid-reorder
			// would collide on the unique index. A missing product falls through
			// to an insert that matches nothing.
			if _, err := q.LockProductCatalogue(ctx, slug); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if err := q.AttachProductImage(ctx, db.AttachProductImageParams{
				Slug: slug, StorageKey: digest, AltText: alt, AltTextEn: altEn,
				Width: width, Height: height, OptionValueID: shows,
			}); err != nil {
				return imageOptionRefusal(err)
			}
			return nil
		})
}

func (s *Store) SetImageOption(ctx context.Context, slug, key, optionValue string) error {
	shows, err := optionValueRef(optionValue)
	if err != nil {
		return err
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetImageOption, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": key, "option_value": shows},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetProductImageOptionValue(ctx, db.SetProductImageOptionValueParams{
				Slug: slug, StorageKey: key, OptionValueID: shows,
			})
			if err != nil {
				return imageOptionRefusal(err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

func optionValueRef(raw string) (uuid.NullUUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.NullUUID{}, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.NullUUID{}, ErrNotThisProductsOption
	}
	return uuid.NullUUID{UUID: id, Valid: true}, nil
}

// imageOptionRefusal names the one refusal a staff member can act on: the
// composite key binding the value to the image's own product.
func imageOptionRefusal(err error) error {
	if pgerr.IsConstraint(err, "product_images_option_value_fk") {
		return ErrNotThisProductsOption
	}
	return pgerr.WrapRefusal(err, ErrRefused)
}

// DetachImage removes one from a product; the shared media object is not deleted.
func (s *Store) DetachImage(ctx context.Context, slug, digest string) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionDetachImage, Table: "product_images", ID: uuid.NullUUID{},
		Before: map[string]any{"product": slug, "digest": digest},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.DetachProductImage(ctx, db.DetachProductImageParams{
				Slug: slug, StorageKey: digest,
			})
			if err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// ImageMove is where an image goes in a product's order. The first image is the
// cover: every card and /compare show it. The product page leads with the chosen
// option value's photographs and then untagged ones, and the cart with the line's.
type ImageMove string

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
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionMoveImage, Table: "product_images", ID: uuid.NullUUID{},
		After: map[string]any{"product": slug, "digest": digest, "move": string(move), "order": &order},
	},
		func(ctx context.Context, q *db.Queries) error {
			// The product's catalogue lock, the one variant creation takes: it
			// serialises the writers that assign positions, attach and move.
			if _, err := q.LockProductCatalogue(ctx, slug); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrNotFound
				}
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
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			if err := q.SetProductImageOrder(ctx, ids); err != nil {
				return pgerr.WrapRefusal(err, ErrRefused)
			}
			return nil
		})
}

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

func (s *Store) Images(ctx context.Context, slug string) ([]admin.Image, error) {
	rows, err := s.q.AdminProductImages(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("read product images: %w", err)
	}
	out := make([]admin.Image, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		img := admin.Image{
			Key: r.StorageKey, Alt: r.AltText,
			Width: r.Width.Int32, Height: r.Height.Int32,
		}
		if r.OptionValueID.Valid {
			img.OptionValueID = r.OptionValueID.UUID.String()
		}
		out = append(out, img)
	}
	return out, nil
}
