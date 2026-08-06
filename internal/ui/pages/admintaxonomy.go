package pages

import "strconv"

// AdminTaxon is one brand or category as the back office sees it.
type AdminTaxon struct {
	Slug string
	Name string
	// NameEn is the English name, empty for one nobody has translated. Only a
	// CATEGORY has one: a category name is in the header of every page, and a
	// brand name is a proper noun that reads the same in both languages.
	NameEn string
	// Products is how many carry it, which is what decides whether it can be
	// removed. Shown rather than hidden: "you cannot delete this" without a
	// number is a refusal a staff member cannot act on.
	Products int64
	// Depth, Children and Parent are a category's; a brand leaves them zero.
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
func (t AdminTaxon) Why() string {
	switch {
	case t.Removable():
		return ""
	case t.Children > 0 && t.Products > 0:
		return "有 " + t.ProductsText() + " 個商品和 " +
			strconv.FormatInt(t.Children, 10) + " 個子分類"
	case t.Children > 0:
		return "有 " + strconv.FormatInt(t.Children, 10) + " 個子分類"
	default:
		return "有 " + t.ProductsText() + " 個商品"
	}
}

// Indent is the nesting depth as a CSS custom property.
//
// A property rather than a class per level, because the tree has no fixed
// depth and a stylesheet cannot enumerate what it does not know.
func (t AdminTaxon) Indent() string { return "--depth:" + strconv.Itoa(t.Depth) }

// AdminTaxonomyView is the brands-and-categories page.
type AdminTaxonomyView struct {
	Brands     []AdminTaxon
	Categories []AdminTaxon
	Notice     string
	// Errors and Draft belong to whichever form was refused; Which says which,
	// so the page reopens the one the staff member was filling in rather than
	// showing an error beside an empty field on the other.
	Which  string
	Errors map[string]string
	Draft  AdminTaxonDraft
}

// AdminTaxonDraft carries a refused form's values back.
type AdminTaxonDraft struct {
	Slug   string
	Name   string
	NameEn string
	Parent string
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
	default:
		// Every caller is a template in this package naming one of the four
		// fields above. A fifth is a typo, and a panic is how it is found in
		// the first render rather than by a silently empty input.
		panic("pages: AdminTaxonomyView.DraftFor: unknown field " + field)
	}
}
