package products

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Choice struct {
	Value string
	Label string
}

type Row struct {
	Slug       string
	Name       string
	Status     product.Status
	StatusText string
	Brand      string
	Category   string
	Variants   int32
	FromCents  int64
	Translated bool
}

func (p Row) From() string {
	if p.Variants == 0 || p.FromCents == 0 {
		return "—"
	}
	return money.TWD(p.FromCents)
}

func (p Row) VariantsText() string { return strconv.FormatInt(int64(p.Variants), 10) }

func (p Row) Href() string { return "/admin/products/" + p.Slug }

func (p Row) Sellable() bool { return p.Status == product.StatusActive && p.Variants > 0 }

type View struct {
	keyset.Bound

	Rows []Row
	// Published is how many products the shop sells now, over every page of Rows.
	Published int64
	Notice    widget.Result
}

func (v View) Empty() bool { return len(v.Rows) == 0 }

type Variant struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	Stock        int32
	SafetyStock  int32
	Active       bool
	Options      []string
}

func (v Variant) OptionText() string { return strings.Join(v.Options, " · ") }

func (v Variant) Price() string { return money.TWD(v.PriceCents) }

func (v Variant) Compare() string {
	if v.CompareCents == 0 {
		return ""
	}
	return money.TWD(v.CompareCents)
}

func (v Variant) StockText(ctx context.Context) string {
	sellable := max(v.Stock-v.SafetyStock, 0)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockOf), v.Stock, sellable)
}

type ProductView struct {
	LabelInput        *LabelInput
	InvoiceTerms      *invoice.LineTerms
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
	Status            product.Status
	StatusText        string
	BrandID           string
	CategoryID        string
	Images            []Image
	Library           []Image
	Brands            []Choice
	Categories        []Choice
	Variants          []Variant
	Options           []Option
	Specs             []Spec
	Errors            map[string]string
	Notice            widget.Result
	VariantDraft      VariantDraft
	SpecDraft         SpecDraft
	Sales             Sales
	Ratings           Ratings
	ImageUploadDraft  ImageUploadDraft
	ImageReuseDraft   ImageReuseDraft
}

type ImageUploadDraft struct {
	Alt, AltEn, OptionValue string
}

type ImageReuseDraft struct {
	Digest, Alt, AltEn string
}

// VariantDraft carries a refused form's exact input back: the variant
// form's, and the two option forms', so a corrected resubmit files what the
// staff member chose and not what the page defaults to.
type VariantDraft struct {
	SKU, Price, Compare                 string
	Safety, ParcelLongest, ParcelSum    string
	ParcelWeight                        string
	OptionValueIDs                      []string
	OptionName, OptionNameEn            string
	ValueOption, Value, ValueEn, Swatch string
}

func (d *VariantDraft) Chose(valueID string) bool {
	return slices.Contains(d.OptionValueIDs, valueID)
}

type Option struct {
	ID     string
	Name   string
	NameEn string
	Values []OptionValue
}

type OptionValue struct {
	// ID is what the variant form posts: two options of one product may share a
	// value, so the link is by id and never by text.
	ID     string
	Value  string
	Label  string
	Option string
}

func (o Option) Translated() bool { return o.NameEn != "" }

func (o Option) HasValues() bool { return len(o.Values) > 0 }

type Spec struct {
	ID      string
	Label   string
	Value   string
	LabelEn string
	ValueEn string
}

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

func (v *ProductView) Action() string {
	if v.IsNew {
		return "/admin/products"
	}
	return "/admin/products/" + v.Slug
}

func (v *ProductView) Title(ctx context.Context) string {
	if v.IsNew {
		return i18n.T(ctx, i18n.KeyAdminPageNewProduct)
	}
	return v.Name
}

func (v *ProductView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *ProductView) Err(f string) string { return v.Errors[f] }

func (v *ProductView) Selected(kind, value string) bool {
	if kind == "brand" {
		return v.BrandID == value
	}
	return v.CategoryID == value
}

func (v *ProductView) CanPublish() bool {
	return !v.IsNew && v.Status != product.StatusActive && len(v.Variants) > 0
}

func (v *ProductView) NeedsVariant() bool { return !v.IsNew && len(v.Variants) == 0 }

// OptionsFrozen reports whether a new option can no longer be added: any SKU,
// active or not, is referenced by order history, so the options are settled.
func (v *ProductView) OptionsFrozen() bool { return !v.IsNew && len(v.Variants) > 0 }

func (v *ProductView) HasOptions() bool { return len(v.Options) > 0 }

func (v *ProductView) HasOptionValue(id string) bool {
	for _, option := range v.Options {
		for _, value := range option.Values {
			if value.ID == id {
				return true
			}
		}
	}
	return false
}

func (v *ProductView) OptionAction() string {
	return "/admin/products/" + v.Slug + "/options"
}

func (v *ProductView) OptionValueAction() string {
	return "/admin/products/" + v.Slug + "/options/values"
}

func (v *ProductView) HasSpecs() bool { return len(v.Specs) > 0 }

func (v *ProductView) SpecAction() string { return "/admin/products/" + v.Slug + "/specs" }

func (v *ProductView) SpecRemoveAction() string {
	return "/admin/products/" + v.Slug + "/specs/remove"
}

func (v *ProductView) HasImages() bool { return len(v.Images) > 0 }

func (v *ProductView) ImageAction() string { return "/admin/products/" + v.Slug + "/images" }

func (v *ProductView) ImageMoveAction() string {
	return "/admin/products/" + v.Slug + "/images/move"
}

func (v *ProductView) ImageRemoveAction() string {
	return "/admin/products/" + v.Slug + "/images/remove"
}

func (v *ProductView) ImageOptionAction() string {
	return "/admin/products/" + v.Slug + "/images/option"
}

func (v *ProductView) ReuseAction() string {
	return "/admin/products/" + v.Slug + "/images/reuse"
}

func (v *ProductView) HasLibrary() bool { return len(v.Library) > 0 }

// Examples shown under the paired fields. They do not follow the reader's locale: which
// language each field takes is fixed by the schema, so the Chinese field shows a Chinese
// example and its English twin an English one.
const (
	altExample         = "白色陶瓷馬克杯，側面，把手朝右"                                   // i18n-exempt: a Chinese example for a field that takes Chinese
	altExampleEn       = "White ceramic mug, side view, handle on the right" // i18n-exempt: an English example for the field that takes English
	optionExample      = "顏色"                                                // i18n-exempt: as above — 顏色, not Colour, is what goes in this box
	optionExampleEn    = "Colour"                                            // i18n-exempt: as above
	optionValExample   = "星霧藍"                                               // i18n-exempt: as above
	optionValExampleEn = "Mist Blue"                                         // i18n-exempt: as above
	swatchExample      = "#1c1c1e"                                           // i18n-exempt: a hex colour, the same in both languages
	specLabelExample   = "容量"                                                // i18n-exempt: as above
	specLabelExampleEn = "Capacity"                                          // i18n-exempt: as above
	specValueExample   = "350 ml"                                            // i18n-exempt: as above
	specValueExampleEn = "350 ml"                                            // i18n-exempt: as above
)

func (v *ProductView) LabelAction() string { return "/admin/products/" + v.Slug + "/label" }

func (v *ProductView) labelInput() *LabelInput {
	if v.LabelInput == nil {
		return &LabelInput{}
	}
	return v.LabelInput
}

func (v *ProductView) InvoiceLineAction() string {
	return "/admin/products/" + v.Slug + "/invoice-line"
}

func (v *ProductView) invoiceTerms() invoice.LineTerms {
	if v.InvoiceTerms == nil {
		return invoice.LineTerms{TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}
	}
	return *v.InvoiceTerms
}

type Image struct {
	Photo picture.Image
	Alt   string
	// OptionValueID is the option value this photograph shows, empty when it
	// shows the product whichever value is chosen.
	OptionValueID string
}

// Key is the name the editor's forms post back for the photograph: an upload's
// digest or a bundled file's name. product_images_bundled_key_format keeps the
// two from colliding, so the store matches it against either column.
func (i Image) Key() string {
	if i.Photo.Digest != "" {
		return i.Photo.Digest
	}
	return i.Photo.BundledKey
}

func (i Image) URL() string { return i.Photo.URL() }

// Srcset offers the 400px rendition, which is what the back office's 80 and 120
// pixel tiles need; the original is a multi-megabyte download for either.
func (i Image) Srcset() string { return i.Photo.Srcset() }

func (v *ProductView) VariantRefusal() widget.Refusal {
	fields := make([]widget.RefusalField, 0, 7+len(v.Options))
	fields = append(fields, widget.RefusalField{Key: "sku", ID: "v-sku"}, widget.RefusalField{Key: "price", ID: "v-price"}, widget.RefusalField{Key: "compare", ID: "v-compare"})
	if len(v.Options) > 0 {
		fields = append(fields, widget.RefusalField{Key: "options", ID: "v-opt-" + v.Options[0].ID})
	}
	fields = append(fields, widget.RefusalField{Key: "safety", ID: "v-safety"}, widget.RefusalField{Key: "parcel_longest", ID: "v-longest"}, widget.RefusalField{Key: "parcel_sum", ID: "v-sum"}, widget.RefusalField{Key: "parcel_weight", ID: "v-weight"})
	return widget.SummarizeRefusal(i18n.KeyAdminProdVariantAdd, v.Errors, fields...)
}
