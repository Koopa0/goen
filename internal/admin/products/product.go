package products

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

const (
	maxSlugRunes        = 120
	maxNameRunes        = 200
	maxSummaryRunes     = 500
	maxDescriptionRunes = 20000
	MaxWarrantyMonths   = 120
)

type Form struct {
	Slug          string
	Name          string
	Summary       string
	Description   string
	NameEn        string
	SummaryEn     string
	DescriptionEn string
	WarrantyNote  string
	// WarrantyMonthsRaw survives a refused parse so the form can show the exact input.
	WarrantyMonthsRaw string
	// WarrantyMonths is 0 when no term is stated; the query stores that as NULL.
	WarrantyMonths int32
	BrandID        string
	CategoryID     string
}

func (f *Form) Validate(ctx context.Context) map[string]string {
	f.Slug = strings.ToLower(strings.TrimSpace(f.Slug))
	f.Name = strings.TrimSpace(f.Name)
	f.NameEn = strings.TrimSpace(f.NameEn)
	f.SummaryEn = strings.TrimSpace(f.SummaryEn)
	f.DescriptionEn = strings.TrimSpace(f.DescriptionEn)
	f.Summary = strings.TrimSpace(f.Summary)
	f.Description = strings.TrimSpace(f.Description)
	f.WarrantyNote = strings.TrimSpace(f.WarrantyNote)

	errs := map[string]string{}
	if !web.ValidSlug(f.Slug) || utf8.RuneCountInString(f.Slug) > maxSlugRunes {
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
	if _, err := uuid.Parse(f.BrandID); f.BrandID != "" && err != nil {
		errs["brand"] = i18n.T(ctx, i18n.KeyFormBrandInvalid)
	}
	if _, err := uuid.Parse(f.CategoryID); err != nil {
		errs["category"] = i18n.T(ctx, i18n.KeyFormCategoryRequired)
	}
	return errs
}

// listPosition is a reader's place in the product list. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type listPosition struct {
	At time.Time
	ID uuid.UUID
}

func (s *Store) List(ctx context.Context, after ...string) (admin.ProductsView, error) {
	scope := "/admin/products"
	from, resumed := web.ResumeKeyset(scope, after, func(p listPosition) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminProducts(ctx, db.AdminProductsParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.ProductsView{}, fmt.Errorf("read products: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminProductsRow) string { return r.PageCursor })
	view := admin.ProductsView{Bound: bound}
	if view.Published, err = s.q.PublishedProductCount(ctx); err != nil {
		return admin.ProductsView{}, fmt.Errorf("count published products: %w", err)
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.Product{
			Slug: r.Slug, Name: r.Name, Status: pages.ProductStatus(r.Status),
			StatusText: statusLabel(ctx, r.Status),
			Brand:      r.Brand, Category: r.Category,
			Variants: r.Variants, FromCents: r.FromCents,
			Translated: r.Translated,
		})
	}
	return view, nil
}

func (s *Store) Product(ctx context.Context, slug string) (admin.ProductView, error) {
	p, err := s.q.AdminProduct(ctx, slug)
	if err != nil {
		return admin.ProductView{}, productReadError(slug, err)
	}
	view := admin.ProductView{
		Slug: p.Slug, Name: p.Name, Summary: p.Summary,
		InvoiceTerms: &invoice.LineTerms{TaxType: invoice.TaxType(p.TaxType), Unit: invoice.ItemUnit(p.InvoiceUnit)},
		LabelInput:   productLabelInput(&p),
		Description:  p.Description, WarrantyNote: p.WarrantyNote,
		NameEn: p.NameEn, SummaryEn: p.SummaryEn, DescriptionEn: p.DescriptionEn,
		WarrantyMonths: p.WarrantyMonths,
		Status:         pages.ProductStatus(p.Status), StatusText: statusLabel(ctx, p.Status),
		BrandID: brandFormValue(p.BrandID), CategoryID: p.CategoryID.String(),
	}
	variants, err := s.q.AdminProductVariants(ctx, p.ID)
	if err != nil {
		return admin.ProductView{}, fmt.Errorf("read variants: %w", err)
	}
	for i := range variants {
		v := &variants[i]
		view.Variants = append(view.Variants, admin.ProductVariant{
			SKU: v.SKU, PriceCents: v.PriceCents,
			CompareCents: v.CompareAtPriceCents.Int64,
			Stock:        v.StockQuantity, SafetyStock: v.SafetyStock,
			Active: v.IsActive,
		})
	}
	links, err := s.q.AdminVariantOptionValues(ctx, slug)
	if err != nil {
		return admin.ProductView{}, fmt.Errorf("read variant options: %w", err)
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
		return admin.ProductView{}, fmt.Errorf("read options: %w", err)
	}
	for i := range opts {
		o := &opts[i]
		item := admin.Option{
			ID: o.ID.String(), Name: o.Name, NameEn: o.NameEn,
		}
		for j := 0; j < len(o.Values) && j < len(o.ValueLabels) &&
			j < len(o.ValueIds); j++ {
			item.Values = append(item.Values, admin.OptionValue{
				ID: o.ValueIds[j], Value: o.Values[j],
				Label: o.ValueLabels[j], Option: item.Name,
			})
		}
		view.Options = append(view.Options, item)
	}

	specs, err := s.q.AdminProductSpecs(ctx, slug)
	if err != nil {
		return admin.ProductView{}, fmt.Errorf("read specs: %w", err)
	}
	for i := range specs {
		sp := &specs[i]
		view.Specs = append(view.Specs, admin.Spec{
			ID: sp.ID.String(), Label: sp.Label, Value: sp.Value,
			LabelEn: sp.LabelEn, ValueEn: sp.ValueEn,
		})
	}
	if err := s.loadChoices(ctx, &view); err != nil {
		return admin.ProductView{}, err
	}
	standingErr := s.standing(ctx, p.ID, time.Now(), &view)
	return view, standingErr
}

func productReadError(slug string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read product %s: %w", slug, err)
}

func (s *Store) NewForm(ctx context.Context) (admin.ProductView, error) {
	view := admin.ProductView{IsNew: true}
	if err := s.loadChoices(ctx, &view); err != nil {
		return admin.ProductView{}, err
	}
	return view, nil
}

func (s *Store) loadChoices(ctx context.Context, view *admin.ProductView) error {
	brands, err := s.q.AdminBrands(ctx)
	if err != nil {
		return fmt.Errorf("read brands: %w", err)
	}
	for i := range brands {
		view.Brands = append(view.Brands, admin.Choice{
			Value: brands[i].ID.String(), Label: brands[i].Name,
		})
	}
	cats, err := s.q.AdminCategories(ctx)
	if err != nil {
		return fmt.Errorf("read categories: %w", err)
	}
	for i := range cats {
		c := &cats[i]
		view.Categories = append(view.Categories, admin.Choice{
			Value: c.ID.String(),
			Label: strings.Repeat("　", int(c.Depth)) + c.Name,
		})
	}
	return nil
}

func brandFormValue(id uuid.NullUUID) string {
	if !id.Valid {
		return ""
	}
	return id.UUID.String()
}

func brandFromForm(raw string) uuid.NullUUID {
	if raw == "" {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: uuid.MustParse(raw), Valid: true}
}

// Create adds a product as a DRAFT. Publishing is its own decision.
func (s *Store) Create(ctx context.Context, f *Form) (slug string, fieldErrs map[string]string, err error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return "", errs, nil
	}
	brandID, categoryID := brandFromForm(f.BrandID), uuid.MustParse(f.CategoryID)

	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateProduct, Table: "products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"name": f.Name, "slug": f.Slug, "brand_id": brandID},
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
		if pgerr.IsConstraint(err, "products_slug_key") {
			return "", map[string]string{"slug": i18n.T(ctx, i18n.KeyFormSlugTakenProduct)}, nil
		}
		if pgerr.IsConstraint(err, "products_warranty_months_sane") {
			return "", map[string]string{"warranty_months": i18n.T(ctx, i18n.KeyFormWarrantyMonths)}, nil
		}
		return "", nil, fmt.Errorf("create product: %w", err)
	}
	return slug, nil, nil
}

// Update edits a product's own fields. Its status is a separate write.
func (s *Store) Update(ctx context.Context, f *Form) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	brandID := brandFromForm(f.BrandID)
	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionUpdateProduct, Table: "products", ID: uuid.NullUUID{},
		After: map[string]any{
			"slug": f.Slug, "name": f.Name,
			"brand_id": brandID, "category_id": f.CategoryID,
			"warranty_months": f.WarrantyMonths,
		},
	}, func(ctx context.Context, q *db.Queries) error {
		n, updateErr := q.UpdateProduct(ctx, db.UpdateProductParams{
			BrandID:    brandID,
			CategoryID: uuid.MustParse(f.CategoryID),
			Slug:       f.Slug, Name: f.Name, Summary: f.Summary,
			Description: f.Description, WarrantyNote: f.WarrantyNote,
			NameEn: f.NameEn, SummaryEn: f.SummaryEn, DescriptionEn: f.DescriptionEn,
			WarrantyMonths: f.WarrantyMonths,
		})
		if updateErr != nil {
			return updateErr
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if pgerr.IsConstraint(err, "products_warranty_months_sane") {
			return map[string]string{"warranty_months": i18n.T(ctx, i18n.KeyFormWarrantyMonths)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update product %s: %w", f.Slug, err)
	}
	return nil, nil
}

// statuses is the catalogue lifecycle, stated once. products_status_known
// is the authority; value and label stay together so a fourth state cannot be
// admitted by the write and left unlabelled on the page.
var statuses = [...]struct {
	value string
	label i18n.Key
}{
	{"draft", i18n.KeyAdminProductDraft},
	{"active", i18n.KeyAdminProductActive},
	{"archived", i18n.KeyAdminProductArchived},
}

func (s *Store) SetStatus(ctx context.Context, slug, status string) error {
	if !knownStatus(status) {
		return ErrRefused
	}
	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionPublishProduct, Table: "products", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "status": status},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetProductStatus(ctx, db.SetProductStatusParams{
				Slug: slug, Status: status,
			})
			if err != nil {
				return fmt.Errorf("set status of %s: %w", slug, err)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
	// products_active_has_variant is deferred, so publishing a product with
	// nothing to sell is refused at COMMIT, which audit.Run reports outside the
	// work; the refusal is read off the whole result.
	return pgerr.WrapRefusal(err, ErrRefused)
}

func knownStatus(s string) bool {
	for _, status := range statuses {
		if status.value == s {
			return true
		}
	}
	return false
}

func statusLabel(ctx context.Context, s string) string {
	for _, status := range statuses {
		if status.value == s {
			return i18n.T(ctx, status.label)
		}
	}
	// Every writer goes through knownStatus, so this is a schema the
	// binary was not built for rather than anything a request can produce.
	panic("admin: no label for product status " + s)
}

// SpecLabelRunes and SpecValueRunes mirror the product_specs CHECKs, in RUNES.
const (
	SpecLabelRunes = 40
	SpecValueRunes = 200
)

type SpecDraft struct {
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

func (s *Store) AddSpec(ctx context.Context, slug string, d SpecDraft) (map[string]string, error) {
	label, value := strings.TrimSpace(d.Label), strings.TrimSpace(d.Value)
	labelEn, valueEn := strings.TrimSpace(d.LabelEn), strings.TrimSpace(d.ValueEn)
	errs := map[string]string{}
	switch {
	case label == "":
		errs["spec_label"] = i18n.T(ctx, i18n.KeyFormSpecLabel)
	case utf8.RuneCountInString(label) > SpecLabelRunes:
		errs["spec_label"] = i18n.T(ctx, i18n.KeyFormSpecLabelLong)
	}
	switch {
	case value == "":
		errs["spec_value"] = i18n.T(ctx, i18n.KeyFormSpecValue)
	case utf8.RuneCountInString(value) > SpecValueRunes:
		errs["spec_value"] = i18n.T(ctx, i18n.KeyFormSpecValueLong)
	}
	if utf8.RuneCountInString(labelEn) > SpecLabelRunes {
		errs["spec_label_en"] = i18n.T(ctx, i18n.KeyFormSpecLabelEnLong)
	}
	if utf8.RuneCountInString(valueEn) > SpecValueRunes {
		errs["spec_value_en"] = i18n.T(ctx, i18n.KeyFormSpecValueEnLong)
	}
	if len(errs) > 0 {
		return errs, nil
	}

	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAddSpec, Table: "product_specs", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "label": label},
	}, func(ctx context.Context, q *db.Queries) error {
		if err := q.LockProductSpecAppendPosition(ctx, slug); err != nil {
			return err
		}
		if _, err := q.AddProductSpec(ctx, db.AddProductSpecParams{
			Slug: slug, Label: label, Value: value,
			LabelEn: labelEn, ValueEn: valueEn,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return pgerr.WrapRefusal(err, ErrRefused)
		}
		return nil
	})
	if pgerr.IsConstraint(err, "product_specs_label_key") {
		return map[string]string{
			"spec_label": i18n.T(ctx, i18n.KeyFormSpecLabelDuplicate),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Store) RemoveSpec(ctx context.Context, slug, id string) error {
	specID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRemoveSpec, Table: "product_specs", ID: audit.EntityID(specID),
		Before: map[string]any{"slug": slug}, After: nil,
	}, func(ctx context.Context, q *db.Queries) error {
		rows, err := q.RemoveProductSpec(ctx, db.RemoveProductSpecParams{
			Slug: slug, SpecID: specID,
		})
		if err != nil {
			return fmt.Errorf("remove spec: %w", pgerr.WrapRefusal(err, ErrRefused))
		}
		if rows == 0 {
			return ErrNotFound
		}
		return nil
	})
}

const MaxOptionNameRunes = 40

type OptionDraft struct {
	OptionID string
	Name     string
	NameEn   string
	// SwatchHex is the colour a value shows as, empty where it is not a colour.
	// Only AddOptionValue reads it; an option is an axis and has no colour.
	SwatchHex string
}

func (s *Store) AddOption(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(ctx, name, nameEn, "option"); len(errs) > 0 {
		return errs, nil
	}

	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAddOption, Table: "product_options", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "name": name},
	}, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddProductOption(ctx, db.AddProductOptionParams{
			Slug: slug, Name: name, NameEn: nameEn,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return pgerr.WrapRefusal(err, ErrRefused)
		}
		return nil
	}); err != nil {
		if pgerr.IsConstraint(err, "product_options_before_variants") {
			return map[string]string{"option": i18n.T(ctx, i18n.KeyFormOptionBeforeVariants)}, nil
		}
		if pgerr.IsConstraint(err, "product_options_name_key") {
			return map[string]string{"option": i18n.T(ctx, i18n.KeyFormOptionNameTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return nil, nil
}

// swatchHex is the shape the column's CHECK accepts, applied here so a typo
// comes back as a field error beside the field rather than as a refused write.
var swatchHex = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func (s *Store) AddOptionValue(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	optionID, err := uuid.Parse(d.OptionID)
	if err != nil {
		return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionPick)}, nil
	}
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(ctx, name, nameEn, "value"); len(errs) > 0 {
		return errs, nil
	}
	// Lower-cased before the CHECK sees it, so a shop that types #1C1C1E is
	// storing the same colour as one that types #1c1c1e rather than being
	// refused for the spelling.
	swatch := strings.ToLower(strings.TrimSpace(d.SwatchHex))
	if swatch != "" && !swatchHex.MatchString(swatch) {
		return map[string]string{"swatch_hex": i18n.T(ctx, i18n.KeyFormSwatchHex)}, nil
	}

	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAddOptionValue, Table: "product_option_values", ID: audit.EntityID(optionID),
		Before: nil, After: map[string]any{"slug": slug, "value": name},
	}, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.AddProductOptionValue(ctx, db.AddProductOptionValueParams{
			Slug: slug, OptionID: optionID, Value: name, ValueEn: nameEn,
			SwatchHex: swatch,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return pgerr.WrapRefusal(err, ErrRefused)
		}
		return nil
	}); err != nil {
		if pgerr.IsConstraint(err, "product_option_values_value_key") {
			return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionValueTaken)}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"value": i18n.T(ctx, i18n.KeyFormOptionMissing)}, nil
		}
		return nil, err
	}
	return nil, nil
}

// optionConstraints names the form field each option constraint speaks for,
// keyed on ConstraintName: a PgError's message never carries it. Each entry
// mirrors a CHECK the code above also applies; the database is the second line,
// and answers when a check here is removed or a column gains a rule, which
// would otherwise read as a product that does not exist.
var optionConstraints = map[string]struct {
	field   string
	message i18n.Key
}{
	"product_options_name_present":           {"option", i18n.KeyFormOptionName},
	"product_option_values_value_present":    {"value", i18n.KeyFormOptionName},
	"product_option_values_swatch_hex_shape": {"swatch_hex", i18n.KeyFormSwatchHex},
}

// optionRefusal is the field error a refused option write comes back as, or nil
// when the failure names no field the form can mark — a missing product, or an
// infrastructure error, which are not things a staff member can retype.
func optionRefusal(ctx context.Context, err error) map[string]string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return nil
	}
	named, ok := optionConstraints[pgErr.ConstraintName]
	if !ok {
		return nil
	}
	return map[string]string{named.field: i18n.T(ctx, named.message)}
}

func optionErrors(ctx context.Context, name, nameEn, field string) map[string]string {
	errs := map[string]string{}
	switch {
	case name == "":
		errs[field] = i18n.T(ctx, i18n.KeyFormOptionName)
	case utf8.RuneCountInString(name) > MaxOptionNameRunes:
		errs[field] = i18n.T(ctx, i18n.KeyFormOptionNameLong)
	}
	if utf8.RuneCountInString(nameEn) > MaxOptionNameRunes {
		errs[field+"_en"] = i18n.T(ctx, i18n.KeyFormOptionNameEnLong)
	}
	return errs
}
