package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// slugFormat mirrors products_slug_format.
var slugFormat = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Form field maxima. The schema does not cap these, and they count RUNES: a byte
// limit would cut a Chinese name at a third of the length.
const (
	maxSlugRunes        = 120
	maxNameRunes        = 200
	maxSummaryRunes     = 500
	maxDescriptionRunes = 20000
	maxSKURunes         = 60
	// MaxWarrantyMonths mirrors products_warranty_months_sane.
	MaxWarrantyMonths = 120

	// MaxPriceCents is the ceiling every money column carries.
	MaxPriceCents = 10000000000
)

// ProductForm is what the back office submits to create or edit a product.
type ProductForm struct {
	Slug        string
	Name        string
	Summary     string
	Description string
	// The English copy, each half optional. An empty box clears what was there.
	NameEn        string
	SummaryEn     string
	DescriptionEn string
	WarrantyNote  string
	// WarrantyMonths is 0 when the shop has not stated a term, which the query
	// stores as NULL and warranty registration then refuses.
	WarrantyMonths int32
	BrandID        string
	CategoryID     string
}

// Validate refuses what the schema would, in the chrome language.
func (f *ProductForm) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Name = strings.TrimSpace(f.Name)
	f.NameEn = strings.TrimSpace(f.NameEn)
	f.SummaryEn = strings.TrimSpace(f.SummaryEn)
	f.DescriptionEn = strings.TrimSpace(f.DescriptionEn)
	f.Summary = strings.TrimSpace(f.Summary)
	f.Description = strings.TrimSpace(f.Description)
	f.WarrantyNote = strings.TrimSpace(f.WarrantyNote)

	errs := map[string]string{}
	if !slugFormat.MatchString(f.Slug) || utf8.RuneCountInString(f.Slug) > maxSlugRunes {
		errs["slug"] = i18n.T(ctx, i18n.KeyFormSlugFormatExample)
	}
	if f.Name == "" || utf8.RuneCountInString(f.Name) > maxNameRunes {
		errs["name"] = i18n.T(ctx, i18n.KeyFormProductName)
	}
	if utf8.RuneCountInString(f.Summary) > maxSummaryRunes {
		errs["summary"] = i18n.T(ctx, i18n.KeyFormProductSummaryLong)
	}
	if utf8.RuneCountInString(f.Description) > maxDescriptionRunes {
		errs["description"] = i18n.T(ctx, i18n.KeyFormProductDescriptionLong)
	}
	if utf8.RuneCountInString(f.NameEn) > maxNameRunes {
		errs["name_en"] = i18n.T(ctx, i18n.KeyFormProductNameEnLong)
	}
	if utf8.RuneCountInString(f.SummaryEn) > maxSummaryRunes {
		errs["summary_en"] = i18n.T(ctx, i18n.KeyFormProductSummaryEnLong)
	}
	if utf8.RuneCountInString(f.DescriptionEn) > maxDescriptionRunes {
		errs["description_en"] = i18n.T(ctx, i18n.KeyFormProductDescriptionEnLong)
	}
	if f.WarrantyMonths < 0 || f.WarrantyMonths > MaxWarrantyMonths {
		errs["warranty_months"] = i18n.T(ctx, i18n.KeyFormWarrantyMonths)
	}
	if _, err := uuid.Parse(f.BrandID); err != nil {
		errs["brand"] = i18n.T(ctx, i18n.KeyFormBrandRequired)
	}
	if _, err := uuid.Parse(f.CategoryID); err != nil {
		errs["category"] = i18n.T(ctx, i18n.KeyFormCategoryRequired)
	}
	return errs
}

// VariantForm is a new variant.
type VariantForm struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	SafetyStock  int32
	// The parcel this variant ships as. Zero is UNMEASURED, stored as NULL: an
	// absent measurement refuses no shipping method, a present one can.
	ParcelLongestMM int32
	ParcelSumMM     int32
	ParcelWeightG   int32
	// OptionValues is one value id per option the product declares, in the order
	// the form rendered them.
	OptionValues []string
}

// Validate refuses what the schema would.
//
// Stock is absent because admin's INSERT grant does not include stock_quantity:
// a new variant starts at zero and stock arrives through the adjustment form.
func (f *VariantForm) Validate(ctx context.Context) map[string]string {
	f.SKU = strings.ToUpper(strings.TrimSpace(f.SKU))

	errs := map[string]string{}
	if f.SKU == "" || utf8.RuneCountInString(f.SKU) > maxSKURunes {
		errs["sku"] = i18n.T(ctx, i18n.KeyFormSKURequired)
	}
	if f.PriceCents <= 0 || f.PriceCents > MaxPriceCents {
		errs["price"] = i18n.T(ctx, i18n.KeyFormPricePositive)
	}
	if f.CompareCents != 0 && f.CompareCents <= f.PriceCents {
		errs["compare"] = i18n.T(ctx, i18n.KeyFormCompareHigher)
	}
	if f.SafetyStock < 0 {
		errs["safety"] = i18n.T(ctx, i18n.KeyFormSafetyStock)
	}
	return errs
}

// Products reads the catalogue for the back office.
func (s *Store) Products(ctx context.Context) (pages.AdminProductsView, error) {
	rows, err := s.q.AdminProducts(ctx, PageSize)
	if err != nil {
		return pages.AdminProductsView{}, fmt.Errorf("read products: %w", err)
	}
	view := pages.AdminProductsView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminProduct{
			Slug: r.Slug, Name: r.Name, Status: r.Status,
			StatusText: ProductStatusLabel(ctx, r.Status),
			Brand:      r.Brand, Category: r.Category,
			Variants: r.Variants, FromCents: r.FromCents,
			Translated: r.Translated,
		})
	}
	return view, nil
}

// Product reads one product and everything its form needs.
func (s *Store) Product(ctx context.Context, slug string) (pages.AdminProductView, error) {
	p, err := s.q.AdminProduct(ctx, slug)
	if err != nil {
		return pages.AdminProductView{}, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	view := pages.AdminProductView{
		Slug: p.Slug, Name: p.Name, Summary: p.Summary,
		Description: p.Description, WarrantyNote: p.WarrantyNote,
		NameEn: p.NameEn, SummaryEn: p.SummaryEn, DescriptionEn: p.DescriptionEn,
		WarrantyMonths: p.WarrantyMonths,
		Status:         p.Status, StatusText: ProductStatusLabel(ctx, p.Status),
		BrandID: p.BrandID.String(), CategoryID: p.CategoryID.String(),
	}
	variants, err := s.q.AdminProductVariants(ctx, p.ID)
	if err != nil {
		return pages.AdminProductView{}, fmt.Errorf("read variants: %w", err)
	}
	for i := range variants {
		v := &variants[i]
		view.Variants = append(view.Variants, pages.AdminProductVariant{
			SKU: v.SKU, PriceCents: v.PriceCents,
			CompareCents: v.CompareAtPriceCents.Int64,
			Stock:        v.StockQuantity, SafetyStock: v.SafetyStock,
			Active: v.IsActive,
		})
	}
	links, err := s.q.AdminVariantOptionValues(ctx, slug)
	if err != nil {
		return pages.AdminProductView{}, fmt.Errorf("read variant options: %w", err)
	}
	bySKU := map[string][]string{}
	for i := range links {
		l := &links[i]
		bySKU[l.SKU] = append(bySKU[l.SKU], l.Value)
	}
	for i := range view.Variants {
		view.Variants[i].Options = bySKU[view.Variants[i].SKU]
	}

	opts, err := s.q.AdminProductOptions(ctx, slug)
	if err != nil {
		return pages.AdminProductView{}, fmt.Errorf("read options: %w", err)
	}
	for i := range opts {
		o := &opts[i]
		item := pages.AdminOption{
			ID: o.ID.String(), Name: o.Name, NameEn: o.NameEn,
		}
		// The shortest of the three aggregates bounds the walk: a mismatch must
		// not pair a label with the wrong value.
		for j := 0; j < len(o.Values) && j < len(o.ValueLabels) &&
			j < len(o.ValueIds); j++ {
			item.Values = append(item.Values, pages.AdminOptionValue{
				ID: o.ValueIds[j], Value: o.Values[j],
				Label: o.ValueLabels[j], Option: item.Name,
			})
		}
		view.Options = append(view.Options, item)
	}

	specs, err := s.q.AdminProductSpecs(ctx, slug)
	if err != nil {
		return pages.AdminProductView{}, fmt.Errorf("read specs: %w", err)
	}
	for i := range specs {
		sp := &specs[i]
		view.Specs = append(view.Specs, pages.AdminSpec{
			ID: sp.ID.String(), Label: sp.Label, Value: sp.Value,
			LabelEn: sp.LabelEn, ValueEn: sp.ValueEn,
		})
	}
	if err := s.loadChoices(ctx, &view); err != nil {
		return pages.AdminProductView{}, err
	}
	return view, nil
}

// NewProduct is an empty form with its choices filled in.
func (s *Store) NewProduct(ctx context.Context) (pages.AdminProductView, error) {
	view := pages.AdminProductView{IsNew: true}
	if err := s.loadChoices(ctx, &view); err != nil {
		return pages.AdminProductView{}, err
	}
	return view, nil
}

// loadChoices fills the brand and category selects.
func (s *Store) loadChoices(ctx context.Context, view *pages.AdminProductView) error {
	brands, err := s.q.AdminBrands(ctx)
	if err != nil {
		return fmt.Errorf("read brands: %w", err)
	}
	for i := range brands {
		view.Brands = append(view.Brands, pages.AdminChoice{
			Value: brands[i].ID.String(), Label: brands[i].Name,
		})
	}
	cats, err := s.q.AdminCategories(ctx)
	if err != nil {
		return fmt.Errorf("read categories: %w", err)
	}
	for i := range cats {
		c := &cats[i]
		view.Categories = append(view.Categories, pages.AdminChoice{
			Value: c.ID.String(),
			Label: strings.Repeat("　", int(c.Depth)) + c.Name,
		})
	}
	return nil
}

// CreateProduct adds a product as a DRAFT. Publishing is its own decision.
func (s *Store) CreateProduct(ctx context.Context, f *ProductForm) (slug string, fieldErrs map[string]string, err error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return "", errs, nil
	}
	brandID, categoryID := uuid.MustParse(f.BrandID), uuid.MustParse(f.CategoryID)

	err = s.audited(ctx, Event{
		Action: ActionCreateProduct, Table: "products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"name": f.Name, "slug": f.Slug},
	},
		func(ctx context.Context, q *db.Queries) error {
			var createErr error
			slug, createErr = q.CreateProduct(ctx, db.CreateProductParams{
				BrandID: brandID, CategoryID: categoryID,
				Slug: f.Slug, Name: f.Name, Summary: f.Summary,
				Description: f.Description, WarrantyNote: f.WarrantyNote,
				NameEn: f.NameEn, SummaryEn: f.SummaryEn,
				DescriptionEn: f.DescriptionEn, WarrantyMonths: f.WarrantyMonths,
			})
			return createErr
		})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "products_slug_key" {
			return "", map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenProduct)}, nil
		}
		return "", nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return slug, nil, nil
}

// UpdateProduct edits a product's own fields. Its status is a separate write.
func (s *Store) UpdateProduct(ctx context.Context, f *ProductForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := s.q.UpdateProduct(ctx, db.UpdateProductParams{
		BrandID:    uuid.MustParse(f.BrandID),
		CategoryID: uuid.MustParse(f.CategoryID),
		Slug:       f.Slug, Name: f.Name, Summary: f.Summary,
		Description: f.Description, WarrantyNote: f.WarrantyNote,
		NameEn: f.NameEn, SummaryEn: f.SummaryEn, DescriptionEn: f.DescriptionEn,
		WarrantyMonths: f.WarrantyMonths,
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetProductStatus publishes, unpublishes or archives.
func (s *Store) SetProductStatus(ctx context.Context, slug, status string) error {
	if status != "draft" && status != "active" && status != "archived" {
		return ErrRefused
	}
	return s.audited(ctx, Event{
		Action: ActionPublishProduct, Table: "products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "status": status},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetProductStatus(ctx, db.SetProductStatusParams{
				Slug: slug, Status: status,
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

// AddVariant adds a variant at zero stock.
func (s *Store) AddVariant(ctx context.Context, slug string, f *VariantForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	chosen, errs := s.chosenOptionValues(ctx, slug, f.OptionValues)
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionCreateVariant, Table: "product_variants", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"product": slug, "sku": f.SKU, "price_cents": f.PriceCents},
	},
		func(ctx context.Context, q *db.Queries) error {
			if createErr := q.CreateVariant(ctx, db.CreateVariantParams{
				Slug: slug, SKU: f.SKU,
				PriceCents: f.PriceCents, CompareAtPriceCents: f.CompareCents,
				SafetyStock:     f.SafetyStock,
				ParcelLongestMm: f.ParcelLongestMM,
				ParcelSumMm:     f.ParcelSumMM,
				ParcelWeightG:   f.ParcelWeightG,
			}); createErr != nil {
				return createErr
			}
			// In the variant's own transaction: a variant without its option
			// values is one the picker cannot reach.
			for _, valueID := range chosen {
				n, linkErr := q.SetVariantOptionValue(ctx, db.SetVariantOptionValueParams{
					SKU: f.SKU, OptionValueID: valueID,
				})
				if linkErr != nil {
					return linkErr
				}
				if n == 0 {
					// The value belongs to another product, or does not exist.
					return ErrNotFound
				}
			}
			return nil
		}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "product_variants_sku_key" {
			return map[string]string{"sku": i18n.T(ctx, i18n.KeyFormSKUTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// ProductStatusLabel is a product's state in the reader's language.
func ProductStatusLabel(ctx context.Context, s string) string {
	switch s {
	case "draft":
		return i18n.T(ctx, i18n.KeyAdminProductDraft)
	case "active":
		return i18n.T(ctx, i18n.KeyAdminProductActive)
	case "archived":
		return i18n.T(ctx, i18n.KeyAdminProductArchived)
	default:
		panic("admin: no label for product status " + s)
	}
}

// SpecLabelRunes and SpecValueRunes bound what the back office may type, in
// RUNES, matching product_specs_label_bounded and product_specs_value_bounded.
const (
	SpecLabelRunes = 40
	SpecValueRunes = 200
)

// SpecDraft is one spec-table row being added, in both languages.
type SpecDraft struct {
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

// AddSpec appends one spec row to a product. The position is computed inside the
// INSERT: two staff members editing one product would otherwise read the same
// maximum and collide on product_specs_position_key.
func (s *Store) AddSpec(ctx context.Context, slug string, d SpecDraft) (map[string]string, error) {
	label, value := strings.TrimSpace(d.Label), strings.TrimSpace(d.Value)
	labelEn, valueEn := strings.TrimSpace(d.LabelEn), strings.TrimSpace(d.ValueEn)
	errs := map[string]string{}
	switch {
	case label == "":
		errs["spec_label"] = i18n.T(ctx, i18n.KeyFormSpecLabel)
	case len([]rune(label)) > SpecLabelRunes:
		errs["spec_label"] = i18n.T(ctx, i18n.KeyFormSpecLabelLong)
	}
	switch {
	case value == "":
		errs["spec_value"] = i18n.T(ctx, i18n.KeyFormSpecValue)
	case len([]rune(value)) > SpecValueRunes:
		errs["spec_value"] = i18n.T(ctx, i18n.KeyFormSpecValueLong)
	}
	if len([]rune(labelEn)) > SpecLabelRunes {
		errs["spec_label_en"] = i18n.T(ctx, i18n.KeyFormSpecLabelEnLong)
	}
	if len([]rune(valueEn)) > SpecValueRunes {
		errs["spec_value_en"] = i18n.T(ctx, i18n.KeyFormSpecValueEnLong)
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionAddSpec, Table: "product_specs", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "label": label},
	}, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddProductSpec(ctx, db.AddProductSpecParams{
			Slug: slug, Label: label, Value: value,
			LabelEn: labelEn, ValueEn: valueEn,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("%w: %s", ErrRefused, err.Error())
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

// RemoveSpec deletes one spec row, scoped to the product in the DELETE's own
// WHERE clause so an id from another product cannot be passed in.
func (s *Store) RemoveSpec(ctx context.Context, slug, id string) error {
	specID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionRemoveSpec, Table: "product_specs", ID: nullableID(specID),
		Before: map[string]any{"slug": slug}, After: nil,
	}, func(ctx context.Context, q *db.Queries) error {
		rows, err := q.RemoveProductSpec(ctx, db.RemoveProductSpecParams{
			Slug: slug, SpecID: specID,
		})
		if err != nil {
			return fmt.Errorf("remove spec: %w", err)
		}
		if rows == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// chosenOptionValues turns the form's option selects into value ids, and refuses a
// variant that does not name one value per axis — one no URL the picker builds can
// resolve, so it would exist as a SKU and appear nowhere on the site.
func (s *Store) chosenOptionValues(ctx context.Context, slug string, raw []string) (
	chosen []uuid.UUID, fieldErrs map[string]string,
) {
	options, err := s.q.ProductOptionCount(ctx, slug)
	if err != nil {
		return nil, map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsUnreadable)}
	}
	chosen = make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if value == "" {
			continue
		}
		id, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return nil, map[string]string{"options": i18n.T(ctx, i18n.KeyFormOptionsInvalid)}
		}
		chosen = append(chosen, id)
	}
	if int64(len(chosen)) != options {
		return nil, map[string]string{
			"options": i18n.T(ctx, i18n.KeyFormVariantNeedsEveryOption),
		}
	}
	return chosen, nil
}

// MaxOptionNameRunes bounds an option name or one of its values.
const MaxOptionNameRunes = 40

// OptionDraft is an option or one of its values, in both languages.
//
// Name is the IDENTITY the picker puts in the URL; NameEn is only a label.
type OptionDraft struct {
	// OptionID is empty when adding the option itself, and names the axis when
	// adding a value to it.
	OptionID string
	Name     string
	NameEn   string
}

// AddOption appends an option — an axis such as colour — to a product.
func (s *Store) AddOption(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(ctx, name, nameEn, "option"); len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionAddOption, Table: "product_options", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "name": name},
	}, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddProductOption(ctx, db.AddProductOptionParams{
			Slug: slug, Name: name, NameEn: nameEn,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return nil
	}); err != nil {
		if takenBy(err, "product_options_name_key") {
			return map[string]string{"option": i18n.T(ctx, i18n.KeyFormOptionNameTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// AddOptionValue appends a value to one of a product's options.
func (s *Store) AddOptionValue(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	optionID, err := uuid.Parse(d.OptionID)
	if err != nil {
		return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionPick)}, nil
	}
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(ctx, name, nameEn, "value"); len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionAddOptionValue, Table: "product_option_values", ID: nullableID(optionID),
		Before: nil, After: map[string]any{"slug": slug, "value": name},
	}, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddProductOptionValue(ctx, db.AddProductOptionValueParams{
			Slug: slug, OptionID: optionID, Value: name, ValueEn: nameEn,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The option belongs to another product, or does not exist.
				return ErrNotFound
			}
			return err
		}
		return nil
	}); err != nil {
		if takenBy(err, "product_option_values_value_key") {
			return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionValueTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionMissing)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// optionErrors is the shared validation; field names which form is being refused.
func optionErrors(ctx context.Context, name, nameEn, field string) map[string]string {
	errs := map[string]string{}
	switch {
	case name == "":
		errs[field] = i18n.T(ctx, i18n.KeyFormOptionName)
	case len([]rune(name)) > MaxOptionNameRunes:
		errs[field] = i18n.T(ctx, i18n.KeyFormOptionNameLong)
	}
	if len([]rune(nameEn)) > MaxOptionNameRunes {
		errs[field+"_en"] = i18n.T(ctx, i18n.KeyFormOptionNameEnLong)
	}
	return errs
}
