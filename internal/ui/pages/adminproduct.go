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
	// Translated reports whether this product has an English name. Shown in the
	// list so the shop can see its own translation debt — an untranslated product
	// reads in Chinese to every English visitor and to nobody at the shop.
	Translated bool
}

// From is the cheapest active variant's price, or a dash when there is nothing
// to sell yet — a product with no variants has no price, and showing NT$0 would
// read as free rather than as unfinished.
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

// Sellable reports whether this product can actually be bought: active, and
// with something to sell.
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
	// Options is the values this variant carries, in the product's axis order.
	// Empty for a single-variant product — and for a variant that was created
	// before the form demanded them, which is the state this column makes visible.
	Options []string
}

// OptionText is the variant's selection as one line, or "" for a product with no
// axes. In Chinese, like the rest of the back office.
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

// StockText is what is on the shelf, and what of it is sellable. Both, because
// safety stock is the difference between "we have twelve" and "you may sell
// two", and a back office showing only the first oversells.
func (v AdminProductVariant) StockText(ctx context.Context) string {
	sellable := v.Stock - v.SafetyStock
	if sellable < 0 {
		sellable = 0
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockOf), v.Stock, sellable)
}

// AdminProductView is the create-or-edit form.
type AdminProductView struct {
	IsNew       bool
	Slug        string
	Name        string
	Summary     string
	Description string
	// The English copy, empty for what nobody has translated.
	NameEn         string
	SummaryEn      string
	DescriptionEn  string
	WarrantyNote   string
	WarrantyMonths int32
	Status         string
	StatusText     string
	BrandID        string
	CategoryID     string
	// Images is what this product currently shows. Empty is normal: a product
	// is born a draft with nothing attached.
	Images []AdminImage
	// Library is what has already been uploaded, for attaching a shot that
	// belongs on more than one product. product_images_storage_key_key is
	// (product_id, storage_key) precisely so that is possible.
	Library    []AdminImage
	Brands     []AdminChoice
	Categories []AdminChoice
	Variants   []AdminProductVariant
	// Options is the product's axes — 顏色, 容量 — and their values. Empty is
	// normal for a single-variant product; a product with options and a variant
	// that names none of them is a variant the picker cannot resolve, which is why
	// the variant form demands one value per option.
	Options []AdminOption
	// Specs is 規格, in the order /compare and the PDP read them. Empty is normal
	// for a new product and was normal for EVERY product the shop created: the
	// table had no writer but the dev seed, so a comparison of two of its own
	// products showed two empty columns.
	Specs  []AdminSpec
	Errors map[string]string
	Notice string
}

// AdminOption is one axis of a product's variants.
type AdminOption struct {
	ID     string
	Name   string
	NameEn string
	// Values are the choices on this axis, with their English labels beside them —
	// empty for one nobody has translated.
	Values []AdminOptionValue
}

// AdminOptionValue is one choice on an axis.
type AdminOptionValue struct {
	// ID is what the variant form posts. The variant is linked by VALUE id, not by
	// text, because two options of one product may legitimately share a value.
	ID     string
	Value  string
	Label  string
	Option string
}

// Translated reports whether this axis reads in English.
func (o AdminOption) Translated() bool { return o.NameEn != "" }

// HasValues reports whether anything can be chosen on this axis yet. An option
// with no values is an axis the picker renders empty.
func (o AdminOption) HasValues() bool { return len(o.Values) > 0 }

// AdminSpec is one 規格 row of a product, in both languages.
type AdminSpec struct {
	ID    string
	Label string
	Value string
	// The English pair, empty for what nobody has translated. Shown on the row so
	// the shop can see which of its specs an English visitor reads in Chinese —
	// otherwise the gap is visible only to that visitor.
	LabelEn string
	ValueEn string
}

// Translated reports whether this row reads in English.
//
// The LABEL decides. A value that is 「6.3 吋 OLED」 barely needs translating and a
// row whose label is English is a row an English visitor can use; demanding both
// would badge rows that are perfectly readable.
func (s AdminSpec) Translated() bool { return s.LabelEn != "" }

// WarrantyMonthsText is the term for the form's number field, empty when the shop
// has not stated one — a 0 in the box would read as "no cover", which is a different
// claim from "we have not said".
func (v *AdminProductView) WarrantyMonthsText() string {
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
//
// A product with no variants has nothing to sell, and publishing it would put a
// page with no price in front of a customer. The form says so instead of
// letting the write fail.
func (v *AdminProductView) CanPublish() bool {
	return !v.IsNew && v.Status != "active" && len(v.Variants) > 0
}

// NeedsVariant reports whether the reason it cannot be published is that it has
// nothing to sell.
func (v *AdminProductView) NeedsVariant() bool { return !v.IsNew && len(v.Variants) == 0 }

// HasOptions reports whether this product has variant axes at all.
func (v *AdminProductView) HasOptions() bool { return len(v.Options) > 0 }

// OptionAction and OptionValueAction are where the two option forms post.
func (v *AdminProductView) OptionAction() string {
	return "/admin/products/" + v.Slug + "/options"
}

// OptionValueAction is where a new value posts.
func (v *AdminProductView) OptionValueAction() string {
	return "/admin/products/" + v.Slug + "/options/values"
}

// HasSpecs reports whether this product states any 規格.
func (v *AdminProductView) HasSpecs() bool { return len(v.Specs) > 0 }

// SpecAction and SpecRemoveAction are where the two spec forms post.
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

// HasLibrary reports whether anything has been uploaded yet. A fresh install
// has nothing, and an empty picker is noise rather than a feature.
func (v *AdminProductView) HasLibrary() bool { return len(v.Library) > 0 }
