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

// AttachImage records an uploaded image against a product.
//
// Alt text is REQUIRED, not optional. A product photograph with no alt text is
// a page a screen reader cannot describe, and the moment it is optional it is
// empty — hero_slides already makes the same demand in the schema
// (hero_slides_image_has_alt), and this is the same rule applied where the
// schema cannot: product_images.alt_text is nullable because the seed predates
// the rule.
// altEn is optional and falls back to alt. A screen reader announces alt text in
// the language <html lang> declares, so an English page with Chinese alt text is
// announced in the wrong voice — the fallback is still better than silence, which
// is why this is not required the way alt is.
func (s *Store) AttachImage(
	ctx context.Context, slug, digest, alt, altEn string, width, height int32,
) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes {
		// English, like every error string in this repository. It is never
		// shown: the handler branches on ErrInvalid and redirects with
		// ?noalt=1, and the SENTENCE the staff member reads is
		// KeyAdminNoticeNoAlt. A Chinese error value here was a customer-facing
		// string only by the sweep's reckoning, and English is what the
		// convention asks of the log line it actually becomes.
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
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

// DetachImage removes one from a product.
//
// The media object itself is not deleted: the same image may be attached
// elsewhere, and the digest is shared by definition. UnreferencedMedia is what
// reclaims one nothing points at.
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
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
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
