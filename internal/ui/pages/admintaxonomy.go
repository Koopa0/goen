package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminTaxon is one brand or category as the back office sees it.
type AdminTaxon struct {
	Slug     string
	Name     string
	NameEn   string
	Products int64
	IconKey  string
	Depth    int
	Children int64
	Parent   string
}

// Translated reports whether this category has an English name.
func (t AdminTaxon) Translated() bool { return t.NameEn != "" }

// Removable reports whether nothing points at it.
func (t AdminTaxon) Removable() bool { return t.Products == 0 && t.Children == 0 }

// ProductsText is how many products carry it.
func (t AdminTaxon) ProductsText() string { return strconv.FormatInt(t.Products, 10) }

// Why explains a refusal, when there is one.
func (t AdminTaxon) Why(ctx context.Context) string {
	switch {
	case t.Removable():
		return ""
	case t.Children > 0 && t.Products > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTaxonomyBoth), t.ProductsText(), t.Children)
	case t.Children > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTaxonomyChildren), t.Children)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTaxonomyProducts), t.ProductsText())
	}
}

// deepestTaxonIndent is the last step app.css draws. The schema rejects only
// cycles, so the tree has no maximum depth, and a row below the last step
// shares it rather than carrying an attribute no rule selects.
const deepestTaxonIndent = 6

// DepthText is the nesting depth as an attribute app.css selects on. It cannot
// be an inline custom property: goen's Content-Security-Policy has no
// 'unsafe-inline' under style-src, so a refused --depth renders the tree flat.
func (t AdminTaxon) DepthText() string {
	if t.Depth > deepestTaxonIndent {
		return strconv.Itoa(deepestTaxonIndent)
	}
	return strconv.Itoa(t.Depth)
}

// AdminTaxonomyView is the brands-and-categories page.
type AdminTaxonomyView struct {
	Brands     []AdminTaxon
	Categories []AdminTaxon
	Notice     string
	Which      string
	Errors     map[string]string
	Draft      AdminTaxonDraft
}

// AdminTaxonDraft carries a refused form's values back.
type AdminTaxonDraft struct {
	Slug    string
	Name    string
	NameEn  string
	Parent  string
	IconKey string
}

// HasErr reports whether this form's field was refused.
func (v AdminTaxonomyView) HasErr(which, field string) bool {
	if v.Which != which {
		return false
	}
	_, ok := v.Errors[field]
	return ok
}

// Err is why.
func (v AdminTaxonomyView) Err(which, field string) string {
	if v.Which != which {
		return ""
	}
	return v.Errors[field]
}

// DraftFor is the value to put back in a field, empty for the other form.
func (v AdminTaxonomyView) DraftFor(which, field string) string {
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
	default:
		panic("pages: AdminTaxonomyView.DraftFor: unknown field " + field)
	}
}
