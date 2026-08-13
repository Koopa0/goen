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
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxTaxonomyNameRunes bounds a brand or category name.
const MaxTaxonomyNameRunes = 60

// ErrInUse is a brand or category something still points at.
//
// Distinct from ErrRefused because the caller's next step differs: this is
// "move the products first", which a staff member can act on, rather than a
// rule they cannot.
var ErrInUse = errors.New("admin: something still uses this")

// TaxonomyForm is a brand or a category being created.
type TaxonomyForm struct {
	Slug string
	Name string
	// NameEn is the English name, empty for one the shop has not translated. A
	// category name is in the header of every page, so this is chrome rather than
	// content — but it is still the SHOP's word for its own department, which is
	// why it is a column here and not a key in the catalogue.
	NameEn string
	// Parent is a category's parent slug, empty for a root. Brands have no
	// parent and leave it empty.
	Parent string
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
	// Blank is legal and means "not translated". Too long is not: it is the same
	// header row, and nullif('') in the query is what turns blank into the NULL the
	// column uses for absence.
	if utf8.RuneCountInString(f.NameEn) > MaxTaxonomyNameRunes {
		errs["name_en"] = i18n.T(ctx, i18n.KeyFormNameEnTooLong)
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
			Slug: c.Slug, Name: c.Name, NameEn: c.NameEn, Products: c.Products,
			Depth: int(c.Depth), Children: c.Children, Parent: c.ParentName,
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
		Action: ActionCreateBrand, Table: "brands",
		After: map[string]any{"slug": f.Slug, "name": f.Name},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateBrand(ctx, db.CreateBrandParams{Slug: f.Slug, Name: f.Name})
		})
	if err != nil {
		if takenBy(err, "brands_slug_key") {
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenBrand)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// CreateCategory adds a category, optionally under a parent.
func (s *Store) CreateCategory(ctx context.Context, f *TaxonomyForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := s.audited(ctx, Event{
		Action: ActionCreateCategory, Table: "categories",
		After: map[string]any{
			"slug": f.Slug, "name": f.Name, "name_en": f.NameEn, "parent": f.Parent,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, createErr := q.CreateCategory(ctx, db.CreateCategoryParams{
				Slug: f.Slug, Name: f.Name, NameEn: f.NameEn, ParentSlug: f.Parent,
			})
			if createErr != nil {
				return createErr
			}
			if n == 0 {
				// A parent was named and does not exist. Refused rather than
				// quietly created at the root, which is what the scalar
				// subquery this replaced would have done.
				return ErrNotFound
			}
			return nil
		})
	if err != nil {
		switch {
		case takenBy(err, "categories_slug_key"):
			return map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenCategory)}, nil
		case errors.Is(err, ErrNotFound),
			takenBy(err, "categories_parent_id_fkey"),
			takenBy(err, "categories_not_own_parent"):
			return map[string]string{"parent": i18n.T(ctx, i18n.KeyFormParentMissing)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// Rename changes a brand's or a category's display name.
//
// The SLUG is never renamed. It is in every URL the search engines have
// indexed and in every link anybody has sent; changing it breaks all of them
// silently, and goen has no redirect table to catch the fallout. A shop that
// truly needs a different slug creates one and moves the products.
// nameEn is the English name and applies to categories only. Empty CLEARS it: a
// shop that added a translation must be able to take it back, and absence is the
// one state the column expresses.
func (s *Store) Rename(ctx context.Context, kind, slug, name, nameEn string) error {
	name, nameEn = strings.TrimSpace(name), strings.TrimSpace(nameEn)
	if name == "" || utf8.RuneCountInString(name) > MaxTaxonomyNameRunes {
		return ErrInvalid
	}
	if utf8.RuneCountInString(nameEn) > MaxTaxonomyNameRunes {
		return ErrInvalid
	}
	action, table := ActionRenameBrand, "brands"
	if kind == "category" {
		action, table = ActionRenameCategory, "categories"
	}
	return s.audited(ctx, Event{
		Action: action, Table: table,
		Before: map[string]any{"slug": slug},
		After:  map[string]any{"name": name, "name_en": nameEn},
	},
		func(ctx context.Context, q *db.Queries) error {
			var n int64
			var err error
			if table == "brands" {
				n, err = q.RenameBrand(ctx, db.RenameBrandParams{Slug: slug, Name: name})
			} else {
				n, err = q.RenameCategory(ctx, db.RenameCategoryParams{
					Slug: slug, Name: name, NameEn: nameEn,
				})
			}
			if err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// Delete removes a brand or category nothing points at.
//
// Emptiness is decided by the statement's own WHERE clause rather than by a
// count read first: a product created between the two would be orphaned. The
// foreign keys are ON DELETE RESTRICT and would refuse it anyway — this makes
// the refusal a row count, so the page can say "move the products first"
// instead of showing a constraint name.
func (s *Store) Delete(ctx context.Context, kind, slug string) error {
	action, table := ActionDeleteBrand, "brands"
	if kind == "category" {
		action, table = ActionDeleteCategory, "categories"
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
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			if n == 0 {
				// Either it does not exist or something still uses it. The
				// second is far more likely and is the one a staff member can
				// act on, so it is what the message says.
				return ErrInUse
			}
			return nil
		})
}

// takenBy reports whether err is this constraint.
//
// Bound to the constraint NAME rather than a substring of the message, which
// rules/error-handling.md forbids and which a locale change would break.
func takenBy(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.ConstraintName == constraint
}
