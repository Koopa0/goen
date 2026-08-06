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
	"github.com/koopa0/goen/internal/ui/pages"
)

// slugFormat is products_slug_format, restated so a bad slug is a message on
// the form rather than a constraint violation the customer never sees.
var slugFormat = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Field maxima. The schema does not cap these; the form does, so a paste of a
// whole datasheet does not become an unbounded row.
//
// Counted in RUNES: a Traditional Chinese product name is three bytes a
// character, and a byte limit would cut it at a third of the length.
const (
	maxSlugRunes        = 120
	maxNameRunes        = 200
	maxSummaryRunes     = 500
	maxDescriptionRunes = 20000
	maxSKURunes         = 60
	// MaxWarrantyMonths mirrors products_warranty_months_sane. Ten years is longer
	// than any consumer electronics warranty and short enough that a typo shows.
	MaxWarrantyMonths = 120

	// MaxPriceCents is the same ceiling every money column carries.
	MaxPriceCents = 10000000000
)

// ProductForm is what the back office submits to create or edit a product.
type ProductForm struct {
	Slug        string
	Name        string
	Summary     string
	Description string
	// The English copy, each half optional. goen never invents a translation —
	// CLAUDE.md's editorial line stands — but a shop that HAS one can say so, and
	// until these columns existed the answer was that the copy stayed Chinese for
	// every visitor. An empty box clears whatever was there.
	NameEn        string
	SummaryEn     string
	DescriptionEn string
	WarrantyNote  string
	// WarrantyMonths is how long this product is covered for, or 0 when the shop has
	// not said. Per PRODUCT because it is not one number: a phone and a braided cable
	// do not carry the same cover, and warranty registration is REFUSED while it is
	// unset — expires_on is NOT NULL, so a default would have goen invent a promise.
	WarrantyMonths int32
	BrandID        string
	CategoryID     string
}

// Validate refuses what the schema would refuse, in the chrome language.
func (f *ProductForm) Validate() map[string]string {
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
		errs["slug"] = "網址代稱只能用小寫英數與連字號,例如 pixelight-9-pro。"
	}
	if f.Name == "" || utf8.RuneCountInString(f.Name) > maxNameRunes {
		errs["name"] = "請填寫商品名稱。"
	}
	if utf8.RuneCountInString(f.Summary) > maxSummaryRunes {
		errs["summary"] = "一句話簡介太長了。"
	}
	if utf8.RuneCountInString(f.Description) > maxDescriptionRunes {
		errs["description"] = "商品說明太長了。"
	}
	// The English copy is optional, so blank is not an error — but the bounds are
	// the same fields on the same pages.
	if utf8.RuneCountInString(f.NameEn) > maxNameRunes {
		errs["name_en"] = "英文名稱太長了。"
	}
	if utf8.RuneCountInString(f.SummaryEn) > maxSummaryRunes {
		errs["summary_en"] = "英文簡介太長了。"
	}
	if utf8.RuneCountInString(f.DescriptionEn) > maxDescriptionRunes {
		errs["description_en"] = "英文說明太長了。"
	}
	// The same range products_warranty_months_sane demands. 0 is "not stated", which
	// the query turns into NULL.
	if f.WarrantyMonths < 0 || f.WarrantyMonths > MaxWarrantyMonths {
		errs["warranty_months"] = "保固月數請填 1 到 120,或留空表示未提供保固。"
	}
	if _, err := uuid.Parse(f.BrandID); err != nil {
		errs["brand"] = "請選擇品牌。"
	}
	if _, err := uuid.Parse(f.CategoryID); err != nil {
		errs["category"] = "請選擇分類。"
	}
	return errs
}

// VariantForm is a new variant.
type VariantForm struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	SafetyStock  int32
	// OptionValues is one value id per option the product declares, in the order
	// the form rendered them. A product with options and a variant that names none
	// is a variant the picker cannot resolve and the listing's one-variant filter
	// never matches — so the form demands one of each rather than allowing a
	// half-specified variant to exist.
	OptionValues []string
}

// Validate refuses what the schema would.
//
// Stock is deliberately absent: a new variant starts at zero and stock arrives
// through the adjustment form, which posts a movement. There is no field here
// to type a number into, because the privilege model would refuse it anyway —
// admin's INSERT grant does not include stock_quantity.
func (f *VariantForm) Validate() map[string]string {
	f.SKU = strings.ToUpper(strings.TrimSpace(f.SKU))

	errs := map[string]string{}
	if f.SKU == "" || utf8.RuneCountInString(f.SKU) > maxSKURunes {
		errs["sku"] = "請填寫 SKU。"
	}
	if f.PriceCents <= 0 || f.PriceCents > MaxPriceCents {
		errs["price"] = "價格必須大於 0。"
	}
	if f.CompareCents != 0 && f.CompareCents <= f.PriceCents {
		errs["compare"] = "原價要高於售價,否則就不是折扣。"
	}
	if f.SafetyStock < 0 {
		errs["safety"] = "安全庫存不能是負數。"
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
			StatusText: ProductStatusLabel(r.Status),
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
		Status:         p.Status, StatusText: ProductStatusLabel(p.Status),
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
	// Which values each variant carries, so the list shows 星霧藍 · 512GB rather
	// than a column of SKUs nobody can tell apart. Read as rows and grouped here:
	// one query for the product, not one per variant.
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
		// The two arrays are aggregated in one ordering by one query; the shorter
		// bounds the walk so a mismatch cannot pair a label with the wrong value.
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
			// Indented by depth: a flat list of a tree hides which category is
			// inside which, and 「手機」 under 「配件」 is a different thing from
			// 「手機」 at the root.
			Label: strings.Repeat("　", int(c.Depth)) + c.Name,
		})
	}
	return nil
}

// CreateProduct adds a product as a DRAFT.
//
// Draft on purpose: products_active_is_published requires a published_at before
// a product may go active, and a product with no variants has no price at all.
// Publishing is its own decision, made once it is ready to be seen.
func (s *Store) CreateProduct(ctx context.Context, f *ProductForm) (slug string, fieldErrs map[string]string, err error) {
	if errs := f.Validate(); len(errs) > 0 {
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
		// products_slug_key is the one a staff member can actually act on,
		// bound to the constraint name rather than a substring of the message.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "products_slug_key" {
			return "", map[string]string{"slug": "這個網址代稱已經有人用了。"}, nil
		}
		return "", nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return slug, nil, nil
}

// UpdateProduct edits a product's own fields. Its status is a separate write,
// because publishing is a decision and renaming is not.
func (s *Store) UpdateProduct(ctx context.Context, f *ProductForm) (map[string]string, error) {
	if errs := f.Validate(); len(errs) > 0 {
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
				// products_active_is_published speaks here, and so does any rule
				// about publishing something with nothing to sell.
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
	if errs := f.Validate(); len(errs) > 0 {
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
				SafetyStock: f.SafetyStock,
			}); createErr != nil {
				return createErr
			}
			// In the SAME transaction as the variant. A variant that exists without
			// its option values is one the picker cannot reach and nothing on the
			// site would show — visible only as a SKU in the back office.
			for _, valueID := range chosen {
				n, linkErr := q.SetVariantOptionValue(ctx, db.SetVariantOptionValueParams{
					SKU: f.SKU, OptionValueID: valueID,
				})
				if linkErr != nil {
					return linkErr
				}
				if n == 0 {
					// The value belongs to another product, or does not exist. The
					// composite foreign key would refuse the first; this catches
					// both as a refusal rather than a 500.
					return ErrNotFound
				}
			}
			return nil
		}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "product_variants_sku_key" {
			return map[string]string{"sku": "這個 SKU 已經有人用了。"}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"options": "規格選項有誤,請重新選擇。"}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// ProductStatusLabel is a product's state in the chrome language.
func ProductStatusLabel(s string) string {
	switch s {
	case "draft":
		return "草稿"
	case "active":
		return "已上架"
	case "archived":
		return "已封存"
	default:
		panic("admin: no label for product status " + s)
	}
}

// SpecLabelRunes and SpecValueRunes bound what the back office may type.
//
// In RUNES, matching product_specs_label_bounded and product_specs_value_bounded,
// which count characters. A byte limit would give a Chinese label a third of the
// room an English one gets, on a site whose specs are written in Chinese.
const (
	SpecLabelRunes = 40
	SpecValueRunes = 200
)

// SpecDraft is one 規格表 row being added, in both languages.
//
// The English pair is optional and separately so: 「螢幕 / 6.3 吋 OLED」 needs the
// label translated and the value barely at all, and making a shop retype a number
// to translate a word is how a translation feature goes unused.
type SpecDraft struct {
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

// AddSpec appends one 規格 row to a product.
//
// The position is computed inside the INSERT rather than read first: two staff
// members editing one product would otherwise both read the same maximum and the
// second would meet product_specs_position_key.
func (s *Store) AddSpec(ctx context.Context, slug string, d SpecDraft) (map[string]string, error) {
	label, value := strings.TrimSpace(d.Label), strings.TrimSpace(d.Value)
	labelEn, valueEn := strings.TrimSpace(d.LabelEn), strings.TrimSpace(d.ValueEn)
	errs := map[string]string{}
	switch {
	case label == "":
		errs["spec_label"] = "請填寫規格名稱"
	case len([]rune(label)) > SpecLabelRunes:
		errs["spec_label"] = "規格名稱太長"
	}
	switch {
	case value == "":
		errs["spec_value"] = "請填寫規格內容"
	case len([]rune(value)) > SpecValueRunes:
		errs["spec_value"] = "規格內容太長"
	}
	if len([]rune(labelEn)) > SpecLabelRunes {
		errs["spec_label_en"] = "英文規格名稱太長"
	}
	if len([]rune(valueEn)) > SpecValueRunes {
		errs["spec_value_en"] = "英文規格內容太長"
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionAddSpec, Table: "product_specs", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"slug": slug, "label": label},
	}, func(ctx context.Context, q *db.Queries) error {
		// The returned id is not carried into the audit row: the row names the
		// product and the label, which is what somebody asking "who added this
		// spec" is looking at. An id nobody can read is not an answer.
		if _, err := q.AddProductSpec(ctx, db.AddProductSpecParams{
			Slug: slug, Label: label, Value: value,
			LabelEn: labelEn, ValueEn: valueEn,
		}); err != nil {
			// A slug that does not exist matches no row, which SQL does not call
			// an error — the defect SetProductStatus already had. :one turns it
			// into ErrNoRows, and this turns that into a refusal.
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

// RemoveSpec deletes one 規格 row, scoped to the product in the DELETE's own
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
// variant that does not name one value per axis.
//
// Demanded here rather than discovered later: a product with two options and a
// variant naming one is resolvable by no URL the picker can build, so it would exist
// as a SKU in the back office and appear nowhere on the site.
func (s *Store) chosenOptionValues(ctx context.Context, slug string, raw []string) (
	chosen []uuid.UUID, fieldErrs map[string]string,
) {
	options, err := s.q.ProductOptionCount(ctx, slug)
	if err != nil {
		return nil, map[string]string{"options": "讀取規格項目失敗,請重試。"}
	}
	chosen = make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if value == "" {
			continue
		}
		id, parseErr := uuid.Parse(value)
		if parseErr != nil {
			return nil, map[string]string{"options": "規格選項有誤,請重新選擇。"}
		}
		chosen = append(chosen, id)
	}
	if int64(len(chosen)) != options {
		return nil, map[string]string{
			"options": "每一個規格項目都要選一個值,否則商品頁的選擇器找不到這個規格。",
		}
	}
	return chosen, nil
}

// MaxOptionNameRunes bounds an option name or one of its values.
//
// The picker renders a value as a swatch: something longer than this is a
// description, and a row of them wraps into a wall.
const MaxOptionNameRunes = 40

// OptionDraft is an option or one of its values, in both languages.
//
// The English half is a LABEL and never an identifier. The variant picker puts the
// choice in the URL and the URL carries the canonical text, so a link shared
// between readers in two languages selects the same variant.
type OptionDraft struct {
	// OptionID is empty when adding the option itself, and names the axis when
	// adding a value to it.
	OptionID string
	Name     string
	NameEn   string
}

// AddOption appends an option — an axis like 顏色 — to a product.
func (s *Store) AddOption(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(name, nameEn, "option"); len(errs) > 0 {
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
			return map[string]string{"option": "這個商品已經有同名的規格項目了。"}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// AddOptionValue appends a value — 星霧藍 — to one of a product's options.
func (s *Store) AddOptionValue(ctx context.Context, slug string, d OptionDraft) (map[string]string, error) {
	optionID, err := uuid.Parse(d.OptionID)
	if err != nil {
		return map[string]string{"value": "請選擇要加值的規格項目。"}, nil
	}
	name, nameEn := strings.TrimSpace(d.Name), strings.TrimSpace(d.NameEn)
	if errs := optionErrors(name, nameEn, "value"); len(errs) > 0 {
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
				// The option belongs to another product, or does not exist. The
				// query resolves product_id FROM the option, so this is the only
				// way that can present itself.
				return ErrNotFound
			}
			return err
		}
		return nil
	}); err != nil {
		if takenBy(err, "product_option_values_value_key") {
			return map[string]string{"value": "這個規格項目已經有同樣的值了。"}, nil
		}
		if errors.Is(err, ErrNotFound) {
			return map[string]string{"value": "找不到這個規格項目。"}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// optionErrors is the shared validation. field names which form is being refused,
// so the page can put the message under the right box.
func optionErrors(name, nameEn, field string) map[string]string {
	errs := map[string]string{}
	switch {
	case name == "":
		errs[field] = "請填寫名稱。"
	case len([]rune(name)) > MaxOptionNameRunes:
		errs[field] = "名稱太長。"
	}
	if len([]rune(nameEn)) > MaxOptionNameRunes {
		errs[field+"_en"] = "英文名稱太長。"
	}
	return errs
}
