package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminChoice is one option in the brand or category select.
type AdminChoice struct {
	Value string
	Label string
}

// AdminProduct is one row in the back-office catalogue.
type AdminProduct struct {
	Slug       string
	Name       string
	Status     string
	StatusText string
	Brand      string
	Category   string
	Variants   int32
	FromCents  int64
	Translated bool
}

// From is the cheapest active variant's price, or a dash when there is nothing to sell.
func (p AdminProduct) From() string {
	if p.Variants == 0 || p.FromCents == 0 {
		return "—"
	}
	return twd(p.FromCents)
}

// VariantsText is how many variants it has.
func (p AdminProduct) VariantsText() string { return strconv.FormatInt(int64(p.Variants), 10) }

// Href is its edit page.
func (p AdminProduct) Href() string { return "/admin/products/" + p.Slug }

// Sellable reports whether this product can actually be bought.
func (p AdminProduct) Sellable() bool { return p.Status == "active" && p.Variants > 0 }

// AdminProductsView is the back-office catalogue.
type AdminProductsView struct {
	Rows   []AdminProduct
	Notice string
}

// Empty reports whether there is nothing to show.
func (v AdminProductsView) Empty() bool { return len(v.Rows) == 0 }

// AdminProductVariant is one variant on the product form.
type AdminProductVariant struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	Stock        int32
	SafetyStock  int32
	Active       bool
	Options      []string
}

// OptionText is the variant's selection as one line.
func (v AdminProductVariant) OptionText() string { return strings.Join(v.Options, " · ") }

// Price is what it sells for.
func (v AdminProductVariant) Price() string { return twd(v.PriceCents) }

// Compare is the struck-through price, or empty when there is none.
func (v AdminProductVariant) Compare() string {
	if v.CompareCents == 0 {
		return ""
	}
	return twd(v.CompareCents)
}

// StockText is what is on the shelf and what of it is sellable.
func (v AdminProductVariant) StockText(ctx context.Context) string {
	sellable := max(v.Stock-v.SafetyStock, 0)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockOf), v.Stock, sellable)
}

// AdminProductView is the create-or-edit form.
type AdminProductView struct {
	IsNew             bool
	Slug              string
	Name              string
	Summary           string
	Description       string
	NameEn            string
	SummaryEn         string
	DescriptionEn     string
	WarrantyNote      string
	WarrantyMonthsRaw string
	WarrantyMonths    int32
	Status            string
	StatusText        string
	BrandID           string
	CategoryID        string
	Images            []AdminImage
	Library           []AdminImage
	Brands            []AdminChoice
	Categories        []AdminChoice
	Variants          []AdminProductVariant
	Options           []AdminOption
	Specs             []AdminSpec
	Errors            map[string]string
	Notice            string
	VariantDraft      AdminVariantDraft
}

// AdminVariantDraft carries a refused variant form's exact text back.
type AdminVariantDraft struct {
	SKU, Price, Compare              string
	Safety, ParcelLongest, ParcelSum string
	ParcelWeight                     string
}

// AdminOption is one axis of a product's variants.
type AdminOption struct {
	ID     string
	Name   string
	NameEn string
	Values []AdminOptionValue
}

// AdminOptionValue is one choice on an axis.
type AdminOptionValue struct {
	// ID is what the variant form posts: two options of one product may share a
	// value, so the link is by id and never by text.
	ID     string
	Value  string
	Label  string
	Option string
}

// Translated reports whether this axis reads in English.
func (o AdminOption) Translated() bool { return o.NameEn != "" }

// HasValues reports whether anything can be chosen on this axis yet.
func (o AdminOption) HasValues() bool { return len(o.Values) > 0 }

// AdminSpec is one spec row of a product, in both languages.
type AdminSpec struct {
	ID      string
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

// Translated reports whether this row reads in English, which the label decides.
func (s AdminSpec) Translated() bool { return s.LabelEn != "" }

// WarrantyMonthsText is the form's numeric text, empty when unstated — 0 claims no cover.
func (v *AdminProductView) WarrantyMonthsText() string {
	if v.WarrantyMonthsRaw != "" {
		return v.WarrantyMonthsRaw
	}
	if v.WarrantyMonths <= 0 {
		return ""
	}
	return strconv.FormatInt(int64(v.WarrantyMonths), 10)
}

// Action is where the form posts.
func (v *AdminProductView) Action() string {
	if v.IsNew {
		return "/admin/products"
	}
	return "/admin/products/" + v.Slug
}

// Title is what the page is called.
func (v *AdminProductView) Title(ctx context.Context) string {
	if v.IsNew {
		return i18n.T(ctx, i18n.KeyAdminPageNewProduct)
	}
	return v.Name
}

// HasErr reports whether a field was refused.
func (v *AdminProductView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why a field was refused.
func (v *AdminProductView) Err(f string) string { return v.Errors[f] }

// Selected reports whether a choice is the current one.
func (v *AdminProductView) Selected(kind, value string) bool {
	if kind == "brand" {
		return v.BrandID == value
	}
	return v.CategoryID == value
}

// CanPublish reports whether publishing is a legal next step.
func (v *AdminProductView) CanPublish() bool {
	return !v.IsNew && v.Status != "active" && len(v.Variants) > 0
}

// NeedsVariant reports whether it cannot be published because it has nothing to sell.
func (v *AdminProductView) NeedsVariant() bool { return !v.IsNew && len(v.Variants) == 0 }

// HasOptions reports whether this product has variant axes at all.
func (v *AdminProductView) HasOptions() bool { return len(v.Options) > 0 }

// OptionAction is where a new option posts.
func (v *AdminProductView) OptionAction() string {
	return "/admin/products/" + v.Slug + "/options"
}

// OptionValueAction is where a new value posts.
func (v *AdminProductView) OptionValueAction() string {
	return "/admin/products/" + v.Slug + "/options/values"
}

// HasSpecs reports whether this product states any specs.
func (v *AdminProductView) HasSpecs() bool { return len(v.Specs) > 0 }

// SpecAction is where a new spec row posts.
func (v *AdminProductView) SpecAction() string { return "/admin/products/" + v.Slug + "/specs" }

// SpecRemoveAction is where a row's remove button posts.
func (v *AdminProductView) SpecRemoveAction() string {
	return "/admin/products/" + v.Slug + "/specs/remove"
}

// HasImages reports whether anything is attached.
func (v *AdminProductView) HasImages() bool { return len(v.Images) > 0 }

// ImageAction is where the upload form posts.
func (v *AdminProductView) ImageAction() string { return "/admin/products/" + v.Slug + "/images" }

// ImageRemoveAction is where the remove form posts.
func (v *AdminProductView) ImageRemoveAction() string {
	return "/admin/products/" + v.Slug + "/images/remove"
}

// ReuseAction is where the picker posts.
func (v *AdminProductView) ReuseAction() string {
	return "/admin/products/" + v.Slug + "/images/reuse"
}

// HasLibrary reports whether anything has been uploaded yet.
func (v *AdminProductView) HasLibrary() bool { return len(v.Library) > 0 }

// Examples for the Chinese half of each paired field. They do not follow the reader's
// locale: which language each field takes is fixed by the schema.
const (
	altExample       = "銀色筆電,螢幕開啟,側面 45 度" // i18n-exempt: a Chinese example for a field that takes Chinese
	optionExample    = "顏色"                // i18n-exempt: as above — 顏色, not Colour, is what goes in this box
	optionValExample = "星霧藍"               // i18n-exempt: as above
	specLabelExample = "螢幕"                // i18n-exempt: as above
	specValueExample = "6.3 吋 OLED"        // i18n-exempt: as above
)
