package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// Taxon is one brand or category as the back office sees it.
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
}

// HeaderHref is the page that holds the category's header photograph.
func (t Taxon) HeaderHref() string { return "/admin/categories/" + t.Slug }

// Translated reports whether this category has an English name.
func (t Taxon) Translated() bool { return t.NameEn != "" }

// Removable reports whether nothing points at it.
func (t Taxon) Removable() bool { return t.Products == 0 && t.Children == 0 }

// ProductsText is how many products carry it.
func (t Taxon) ProductsText() string { return strconv.FormatInt(t.Products, 10) }

// Why explains a refusal, when there is one.
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

// deepestTaxonIndent is the last step app.css draws. The schema rejects only
// cycles, so the tree has no maximum depth, and a row below the last step
// shares it rather than carrying an attribute no rule selects.
const deepestTaxonIndent = 6

// DepthText is the nesting depth as an attribute app.css selects on. It cannot
// be an inline custom property: goen's Content-Security-Policy has no
// 'unsafe-inline' under style-src, so a refused --depth renders the tree flat.
func (t Taxon) DepthText() string {
	if t.Depth > deepestTaxonIndent {
		return strconv.Itoa(deepestTaxonIndent)
	}
	return strconv.Itoa(t.Depth)
}

// TaxonomyView is the brands-and-categories page.
type TaxonomyView struct {
	Brands     []Taxon
	Categories []Taxon
	Notice     string
	Which      string
	Errors     map[string]string
	Draft      TaxonDraft
}

// TaxonDraft carries a refused form's values back.
type TaxonDraft struct {
	Slug    string
	Name    string
	NameEn  string
	Parent  string
	IconKey string
	Tone    string
}

// HasErr reports whether this form's field was refused.
func (v TaxonomyView) HasErr(which, field string) bool {
	if v.Which != which {
		return false
	}
	_, ok := v.Errors[field]
	return ok
}

// Err is why.
func (v TaxonomyView) Err(which, field string) string {
	if v.Which != which {
		return ""
	}
	return v.Errors[field]
}

// DraftFor is the value to put back in a field, empty for the other form.
func (v TaxonomyView) DraftFor(which, field string) string {
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
	default:
		panic("pages: TaxonomyView.DraftFor: unknown field " + field)
	}
}
