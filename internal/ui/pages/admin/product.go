package admin

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/productlabel"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type Choice struct {
	Value string
	Label string
}

type Product struct {
	Slug       string
	Name       string
	Status     pages.ProductStatus
	StatusText string
	Brand      string
	Category   string
	Variants   int32
	FromCents  int64
	Translated bool
}

func (p Product) From() string {
	if p.Variants == 0 || p.FromCents == 0 {
		return "—"
	}
	return money.TWD(p.FromCents)
}

func (p Product) VariantsText() string { return strconv.FormatInt(int64(p.Variants), 10) }

func (p Product) Href() string { return "/admin/products/" + p.Slug }

func (p Product) Sellable() bool { return p.Status == pages.ProductActive && p.Variants > 0 }

type ProductsView struct {
	web.Bound

	Rows []Product
	// Published is how many products the shop sells now, over every page of Rows.
	Published int64
	Notice    components.Result
}

func (v ProductsView) Empty() bool { return len(v.Rows) == 0 }

type ProductVariant struct {
	SKU          string
	PriceCents   int64
	CompareCents int64
	Stock        int32
	SafetyStock  int32
	Active       bool
	Options      []string
}

func (v ProductVariant) OptionText() string { return strings.Join(v.Options, " · ") }

func (v ProductVariant) Price() string { return money.TWD(v.PriceCents) }

func (v ProductVariant) Compare() string {
	if v.CompareCents == 0 {
		return ""
	}
	return money.TWD(v.CompareCents)
}

func (v ProductVariant) StockText(ctx context.Context) string {
	sellable := max(v.Stock-v.SafetyStock, 0)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockOf), v.Stock, sellable)
}

type ProductView struct {
	LabelInput        *productlabel.Input
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
	Status            pages.ProductStatus
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
	Notice            components.Result
	VariantDraft      VariantDraft
	SpecDraft         SpecDraft
	ImageUploadDraft  ProductImageUploadDraft
	ImageReuseDraft   ProductImageReuseDraft
}

type ProductImageUploadDraft struct {
	Alt, AltEn, OptionValue string
}

type ProductImageReuseDraft struct {
	Digest, Alt, AltEn string
}

type SpecDraft struct {
	Label   string
	Value   string
	LabelEn string
	ValueEn string
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
	return !v.IsNew && v.Status != pages.ProductActive && len(v.Variants) > 0
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

// Examples for the Chinese half of each paired field. They do not follow the reader's
// locale: which language each field takes is fixed by the schema.
const (
	altExample       = "白色陶瓷馬克杯，側面，把手朝右" // i18n-exempt: a Chinese example for a field that takes Chinese
	optionExample    = "顏色"              // i18n-exempt: as above — 顏色, not Colour, is what goes in this box
	optionValExample = "星霧藍"             // i18n-exempt: as above
	specLabelExample = "容量"              // i18n-exempt: as above
	specValueExample = "350 ml"          // i18n-exempt: as above
)

func (v *ProductView) LabelAction() string { return "/admin/products/" + v.Slug + "/label" }

func (v *ProductView) labelInput() *productlabel.Input {
	if v.LabelInput == nil {
		return &productlabel.Input{}
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
