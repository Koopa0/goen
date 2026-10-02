package admin

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Choice is one option in the brand or category select.
type Choice struct {
	Value string
	Label string
}

// Product is one row in the back-office catalogue.
type Product struct {
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
func (p Product) From() string {
	if p.Variants == 0 || p.FromCents == 0 {
		return "—"
	}
	return money.TWD(p.FromCents)
}

// VariantsText is how many variants it has.
func (p Product) VariantsText() string { return strconv.FormatInt(int64(p.Variants), 10) }

// Href is its edit page.
func (p Product) Href() string { return "/admin/products/" + p.Slug }

// Sellable reports whether this product can actually be bought.
func (p Product) Sellable() bool { return p.Status == "active" && p.Variants > 0 }

// ProductsView is the back-office catalogue.
type ProductsView struct {
	pages.ListBound

	Rows   []Product
	Notice string
}

// Empty reports whether there is nothing to show.
func (v ProductsView) Empty() bool { return len(v.Rows) == 0 }

// ProductVariant is one variant on the product form.
type ProductVariant struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	Stock        int32
	SafetyStock  int32
	Active       bool
	Options      []string
}

// OptionText is the variant's selection as one line.
func (v ProductVariant) OptionText() string { return strings.Join(v.Options, " · ") }

// Price is what it sells for.
func (v ProductVariant) Price() string { return money.TWD(v.PriceCents) }

// Compare is the struck-through price, or empty when there is none.
func (v ProductVariant) Compare() string {
	if v.CompareCents == 0 {
		return ""
	}
	return money.TWD(v.CompareCents)
}

// StockText is what is on the shelf and what of it is sellable.
func (v ProductVariant) StockText(ctx context.Context) string {
	sellable := max(v.Stock-v.SafetyStock, 0)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockOf), v.Stock, sellable)
}

// ProductView is the create-or-edit form.
type ProductView struct {
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
	Images            []Image
	Library           []Image
	Brands            []Choice
	Categories        []Choice
	Variants          []ProductVariant
	Options           []Option
	Specs             []Spec
	Errors            map[string]string
	Notice            string
	VariantDraft      VariantDraft
}

// VariantDraft carries a refused form's exact input back: the variant
// form's, and the two option forms', so a corrected resubmit files what the
// staff member chose and not what the page defaults to.
type VariantDraft struct {
	SKU, Price, Compare              string
	Safety, ParcelLongest, ParcelSum string
	ParcelWeight                     string
	// OptionValueIDs are the option values the variant form had chosen.
	OptionValueIDs []string
	// The add-axis form.
	OptionName, OptionNameEn string
	// The add-value form: the axis it was filed under, then the value's fields.
	ValueOption, Value, ValueEn, Swatch string
}

// Chose reports whether the refused variant form had picked this option value.
func (d *VariantDraft) Chose(valueID string) bool {
	return slices.Contains(d.OptionValueIDs, valueID)
}

// Option is one axis of a product's variants.
type Option struct {
	ID     string
	Name   string
	NameEn string
	Values []OptionValue
}

// OptionValue is one choice on an axis.
type OptionValue struct {
	// ID is what the variant form posts: two options of one product may share a
	// value, so the link is by id and never by text.
	ID     string
	Value  string
	Label  string
	Option string
}

// Translated reports whether this axis reads in English.
func (o Option) Translated() bool { return o.NameEn != "" }

// HasValues reports whether anything can be chosen on this axis yet.
func (o Option) HasValues() bool { return len(o.Values) > 0 }

// Spec is one spec row of a product, in both languages.
type Spec struct {
	ID      string
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

// Translated reports whether this row reads in English, which the label decides.
func (s Spec) Translated() bool { return s.LabelEn != "" }

// WarrantyMonthsText is the form's numeric text, empty when unstated — 0 claims no cover.
func (v *ProductView) WarrantyMonthsText() string {
	if v.WarrantyMonthsRaw != "" {
		return v.WarrantyMonthsRaw
	}
	if v.WarrantyMonths <= 0 {
		return ""
	}
	return strconv.FormatInt(int64(v.WarrantyMonths), 10)
}

// Action is where the form posts.
func (v *ProductView) Action() string {
	if v.IsNew {
		return "/admin/products"
	}
	return "/admin/products/" + v.Slug
}

// Title is what the page is called.
func (v *ProductView) Title(ctx context.Context) string {
	if v.IsNew {
		return i18n.T(ctx, i18n.KeyAdminPageNewProduct)
	}
	return v.Name
}

// HasErr reports whether a field was refused.
func (v *ProductView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why a field was refused.
func (v *ProductView) Err(f string) string { return v.Errors[f] }

// Selected reports whether a choice is the current one.
func (v *ProductView) Selected(kind, value string) bool {
	if kind == "brand" {
		return v.BrandID == value
	}
	return v.CategoryID == value
}

// CanPublish reports whether publishing is a legal next step.
func (v *ProductView) CanPublish() bool {
	return !v.IsNew && v.Status != "active" && len(v.Variants) > 0
}

// NeedsVariant reports whether it cannot be published because it has nothing to sell.
func (v *ProductView) NeedsVariant() bool { return !v.IsNew && len(v.Variants) == 0 }

// OptionsFrozen reports whether a new option can no longer be added: any SKU,
// active or not, is referenced by order history, so the options are settled.
func (v *ProductView) OptionsFrozen() bool { return !v.IsNew && len(v.Variants) > 0 }

// HasOptions reports whether this product has variant axes at all.
func (v *ProductView) HasOptions() bool { return len(v.Options) > 0 }

// OptionAction is where a new option posts.
func (v *ProductView) OptionAction() string {
	return "/admin/products/" + v.Slug + "/options"
}

// OptionValueAction is where a new value posts.
func (v *ProductView) OptionValueAction() string {
	return "/admin/products/" + v.Slug + "/options/values"
}

// HasSpecs reports whether this product states any specs.
func (v *ProductView) HasSpecs() bool { return len(v.Specs) > 0 }

// SpecAction is where a new spec row posts.
func (v *ProductView) SpecAction() string { return "/admin/products/" + v.Slug + "/specs" }

// SpecRemoveAction is where a row's remove button posts.
func (v *ProductView) SpecRemoveAction() string {
	return "/admin/products/" + v.Slug + "/specs/remove"
}

// HasImages reports whether anything is attached.
func (v *ProductView) HasImages() bool { return len(v.Images) > 0 }

// ImageAction is where the upload form posts.
func (v *ProductView) ImageAction() string { return "/admin/products/" + v.Slug + "/images" }

// ImageMoveAction is where the cover and reorder forms post.
func (v *ProductView) ImageMoveAction() string {
	return "/admin/products/" + v.Slug + "/images/move"
}

// ImageRemoveAction is where the remove form posts.
func (v *ProductView) ImageRemoveAction() string {
	return "/admin/products/" + v.Slug + "/images/remove"
}

// ImageOptionAction is where an attached image's option form posts.
func (v *ProductView) ImageOptionAction() string {
	return "/admin/products/" + v.Slug + "/images/option"
}

// ReuseAction is where the picker posts.
func (v *ProductView) ReuseAction() string {
	return "/admin/products/" + v.Slug + "/images/reuse"
}

// HasLibrary reports whether anything has been uploaded yet.
func (v *ProductView) HasLibrary() bool { return len(v.Library) > 0 }

// Examples for the Chinese half of each paired field. They do not follow the reader's
// locale: which language each field takes is fixed by the schema.
const (
	altExample       = "白色陶瓷馬克杯，側面，把手朝右" // i18n-exempt: a Chinese example for a field that takes Chinese
	optionExample    = "顏色"              // i18n-exempt: as above — 顏色, not Colour, is what goes in this box
	optionValExample = "星霧藍"             // i18n-exempt: as above
	specLabelExample = "容量"              // i18n-exempt: as above
	specValueExample = "350 ml"          // i18n-exempt: as above
)
