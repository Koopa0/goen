package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

type Taxon struct {
	Slug     string
	Name     string
	NameEn   string
	Products int64
	IconKey  string
	Depth    int
	Children int64
	Parent   string
	Tone     string // "" inherits the department's
	// Comparable is a department's own answer; a sub-category takes its
	// department's and shows no control.
	Comparable bool
}

func (t Taxon) HeaderHref() string { return "/admin/categories/" + t.Slug }

func (t Taxon) Translated() bool { return t.NameEn != "" }

func (t Taxon) Removable() bool { return t.Products == 0 && t.Children == 0 }

func (t Taxon) ProductsText() string { return strconv.FormatInt(t.Products, 10) }

func (t Taxon) Why(ctx context.Context) string {
	switch {
	case t.Removable():
		return ""
	case t.Children > 0 && t.Products > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTaxonomyBoth), t.ProductsText(), t.Children)
	case t.Children > 0:
		return i18n.Count(ctx, i18n.KeyAdminTaxonomyChildren, t.Children, t.Children)
	default:
		return i18n.Count(ctx, i18n.KeyAdminTaxonomyProducts, t.Products, t.ProductsText())
	}
}

// deepestTaxonIndent is the last step admin.css draws. The schema rejects only
// cycles, so the tree has no maximum depth, and a row below the last step
// shares it rather than carrying an attribute no rule selects.
const deepestTaxonIndent = 6

// DepthText is the nesting depth as an attribute admin.css selects on. It cannot
// be an inline custom property: goen's Content-Security-Policy has no
// 'unsafe-inline' under style-src, so a refused --depth renders the tree flat.
func (t Taxon) DepthText() string {
	if t.Depth > deepestTaxonIndent {
		return strconv.Itoa(deepestTaxonIndent)
	}
	return strconv.Itoa(t.Depth)
}

type TaxonomyView struct {
	Brands     []Taxon
	Categories []Taxon
	Notice     components.Result
	Which      string
	Errors     map[string]string
	Draft      TaxonDraft
}

type TaxonDraft struct {
	Slug       string
	Name       string
	NameEn     string
	Parent     string
	IconKey    string
	Tone       string
	Comparable bool
}

func (v *TaxonomyView) HasErr(which, field string) bool {
	if v.Which != which {
		return false
	}
	_, ok := v.Errors[field]
	return ok
}

func (v *TaxonomyView) Err(which, field string) string {
	if v.Which != which {
		return ""
	}
	return v.Errors[field]
}

func (v *TaxonomyView) DraftFor(which, field string) string {
	if v.Which != which {
		return ""
	}
	switch field {
	case "slug":
		return v.Draft.Slug
	case "name":
		return v.Draft.Name
	case "name_en":
		return v.Draft.NameEn
	case "parent":
		return v.Draft.Parent
	case "icon_key":
		return v.Draft.IconKey
	case "tone":
		return v.Draft.Tone
	case "comparable":
		if v.Draft.Comparable {
			return "1"
		}
		return ""
	default:
		panic("pages: TaxonomyView.DraftFor: unknown field " + field)
	}
}

// slugExample is a made-up address for the kind's slug field, never one of the
// shop's own brands or categories.
func slugExample(kind string) string {
	if kind == "categories" {
		return "cookware"
	}
	return "north-light"
}
