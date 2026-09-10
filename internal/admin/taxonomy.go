package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/icons"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxTaxonomyNameRunes bounds a brand or category name.
const MaxTaxonomyNameRunes = 60

// ErrInUse is a brand or category something still points at.
var ErrInUse = errors.New("admin: something still uses this")

// TaxonomyForm is a brand or a category being created.
type TaxonomyForm struct {
	Slug    string
	Name    string
	NameEn  string
	Parent  string
	IconKey string
}

// Validate refuses what the schema would, with a message naming the field.
func (f *TaxonomyForm) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Name = strings.TrimSpace(f.Name)
	f.NameEn = strings.TrimSpace(f.NameEn)
	f.Parent = strings.ToLower(strings.TrimSpace(f.Parent))

	errs := map[string]string{}
	if !slugFormat.MatchString(f.Slug) {
		errs["slug"] = i18n.T(ctx, i18n.KeyFormSlugFormat)
	}
	if f.Name == "" || utf8.RuneCountInString(f.Name) > MaxTaxonomyNameRunes {
		errs["name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	if utf8.RuneCountInString(f.NameEn) > MaxTaxonomyNameRunes {
		errs["name_en"] = i18n.T(ctx, i18n.KeyFormNameEnTooLong)
	}
	f.IconKey = strings.TrimSpace(f.IconKey)
	if f.IconKey != "" && !icons.KnownCategory(f.IconKey) {
		errs["icon_key"] = i18n.T(ctx, i18n.KeyFormIconUnknown)
	}
	return errs
}

// Taxonomy reads the brands and the category tree.
func (s *Store) Taxonomy(ctx context.Context) (pages.AdminTaxonomyView, error) {
	brands, err := s.q.ManagedBrands(ctx)
	if err != nil {
		return pages.AdminTaxonomyView{}, fmt.Errorf("read brands: %w", err)
	}
	cats, err := s.q.ManagedCategories(ctx)
	if err != nil {
		return pages.AdminTaxonomyView{}, fmt.Errorf("read categories: %w", err)
	}

	view := pages.AdminTaxonomyView{}
	for i := range brands {
		b := &brands[i]
		view.Brands = append(view.Brands, pages.AdminTaxon{
			Slug: b.Slug, Name: b.Name, Products: b.Products,
		})
	}
	for i := range cats {
		c := &cats[i]
		view.Categories = append(view.Categories, pages.AdminTaxon{
			Slug: c.Slug, Name: c.Name, NameEn: c.NameEn, IconKey: c.IconKey,
			Products: c.Products,
			Depth:    int(c.Depth), Children: c.Children, Parent: c.ParentName,
		})
	}
	return view, nil
}

// CreateBrand adds a brand.
func (s *Store) CreateBrand(ctx context.Context, f *TaxonomyForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: actionCreateBrand, Table: "brands",
		After: map[string]any{"slug": f.Slug, "name": f.Name},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateBrand(ctx, db.CreateBrandParams{Slug: f.Slug, Name: f.Name})
		})
	if err != nil {
		if hasConstraint(err, "brands_slug_key") {
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenBrand)}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// CreateCategory adds a category, optionally under a parent.
func (s *Store) CreateCategory(ctx context.Context, f *TaxonomyForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: actionCreateCategory, Table: "categories",
		After: map[string]any{
			"slug": f.Slug, "name": f.Name, "name_en": f.NameEn,
			"icon_key": f.IconKey, "parent": f.Parent,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, createErr := q.CreateCategory(ctx, db.CreateCategoryParams{
				Slug: f.Slug, Name: f.Name, NameEn: f.NameEn,
				IconKey: f.IconKey, ParentSlug: f.Parent,
			})
			if createErr != nil {
				return createErr
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
	if err != nil {
		switch {
		case hasConstraint(err, "categories_slug_key"):
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenCategory)}, nil
		case errors.Is(err, ErrNotFound),
			hasConstraint(err, "categories_parent_id_fkey"),
			hasConstraint(err, "categories_not_own_parent"):
			return map[string]string{"parent": i18n.T(ctx, i18n.KeyFormParentMissing)}, nil
		case hasConstraint(err, "categories_position_key"):
			// Not a field: position is computed inside the INSERT and never
			// typed, so there is nothing on the form to point at. The second
			// attempt reads a fresh maximum and goes through.
			return map[string]string{"form": i18n.T(ctx, i18n.KeyFormPositionTaken)}, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// Rename changes a display name, never a slug: goen has no redirect table.
func (s *Store) Rename(ctx context.Context, kind, slug, name, nameEn, iconKey string) error {
	name, nameEn = strings.TrimSpace(name), strings.TrimSpace(nameEn)
	iconKey = strings.TrimSpace(iconKey)
	if name == "" || utf8.RuneCountInString(name) > MaxTaxonomyNameRunes {
		return ErrInvalid
	}
	if utf8.RuneCountInString(nameEn) > MaxTaxonomyNameRunes {
		return ErrInvalid
	}
	if iconKey != "" && !icons.KnownCategory(iconKey) {
		return ErrInvalid
	}
	action, table := actionRenameBrand, "brands"
	after := map[string]any{"name": name}
	if kind == "category" {
		action, table = actionRenameCategory, "categories"
		after["name_en"] = nameEn
		after["icon_key"] = iconKey
	}
	return s.audited(ctx, Event{
		Action: action, Table: table,
		Before: map[string]any{"slug": slug},
		After:  after,
	},
		func(ctx context.Context, q *db.Queries) error {
			var n int64
			var err error
			if table == "brands" {
				n, err = q.RenameBrand(ctx, db.RenameBrandParams{Slug: slug, Name: name})
			} else {
				n, err = q.RenameCategory(ctx, db.RenameCategoryParams{
					Slug: slug, Name: name, NameEn: nameEn, IconKey: iconKey,
				})
			}
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// Delete removes a brand or category, decided by the DELETE's own WHERE clause.
func (s *Store) Delete(ctx context.Context, kind, slug string) error {
	action, table := actionDeleteBrand, "brands"
	if kind == "category" {
		action, table = actionDeleteCategory, "categories"
	}
	return s.audited(ctx, Event{
		Action: action, Table: table, Before: map[string]any{"slug": slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			var n int64
			var err error
			if table == "brands" {
				n, err = q.DeleteBrand(ctx, slug)
			} else {
				n, err = q.DeleteCategory(ctx, slug)
			}
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			if n == 0 {
				return ErrInUse
			}
			return nil
		})
}

// hasConstraint reports whether err is this constraint. Bound to ConstraintName: a
// PgError's message never contains it, so a substring search cannot match.
func hasConstraint(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.ConstraintName == constraint
}
