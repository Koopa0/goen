package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxAltRunes = 200

func (s *Store) CategoryHeader(ctx context.Context, slug string) (admin.CategoryView, error) {
	row, err := s.q.AdminCategoryImage(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.CategoryView{}, ErrNotFound
		}
		return admin.CategoryView{}, fmt.Errorf("read category header: %w", err)
	}
	return admin.CategoryView{
		Slug: slug, Name: row.Name, Tone: row.Tone,
		Image: admin.Header{Key: row.ImageKey, Alt: row.ImageAlt, AltEn: row.ImageAltEn, Width: row.ImageWidth},
	}, nil
}

// SetCategoryImage makes a stored upload the category's header photograph. The
// alt text is required: categories_image_has_alt refuses an image without it.
func (s *Store) SetCategoryImage(ctx context.Context, slug, digest, alt, altEn string) error {
	alt, altEn = strings.TrimSpace(alt), strings.TrimSpace(altEn)
	if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes ||
		utf8.RuneCountInString(altEn) > MaxAltRunes {
		return fmt.Errorf("%w: header alt text is required and bounded at %d runes", ErrInvalid, MaxAltRunes)
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetCategoryImage, Table: "categories", ID: uuid.NullUUID{},
		After: map[string]any{"category": slug, "digest": digest, "alt": alt},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCategoryImage(ctx, db.SetCategoryImageParams{
				Slug: strings.TrimSpace(slug), ImageKey: digest, ImageAlt: alt, ImageAltEn: altEn,
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

// ClearCategoryImage removes the photograph; the media object itself stays.
func (s *Store) ClearCategoryImage(ctx context.Context, slug string) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionClearCategoryImage, Table: "categories", ID: uuid.NullUUID{},
		Before: map[string]any{"category": slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.ClearCategoryImage(ctx, strings.TrimSpace(slug))
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}
