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

// Indent is the nesting depth as a CSS custom property.
func (t AdminTaxon) Indent() string { return "--depth:" + strconv.Itoa(t.Depth) }

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

// CategoryIcons is the closed set the home page can draw.
var CategoryIcons = []string{"phone", "laptop", "tablet", "headphones", "watch", "plug", "shield"}

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
