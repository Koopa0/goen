package pages

import (
	"context"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

const (
	previewProducts = 3
	previewMinRows  = 3
	previewMaxRows  = 6
	// StoryColours is how many colours a colour story shows, and the fewest it needs.
	StoryColours = 3
)

// The unit follows the number after a no-break space, which Count writes.
// StatCountOf is a count with the unit k writes for it, kept together as one stat value.
func StatCountOf(ctx context.Context, k i18n.Key, n int64) components.StatValue {
	_, unit, _ := strings.Cut(i18n.Count(ctx, k, n, n), "\u00a0")
	return components.StatCount(n, unit)
}

// DepartmentHead is what a department page says under its band. Notice, Preview and Story
// are nil where the department has nothing to say.
type DepartmentHead struct {
	Products, Categories, Brands int64
	Notice                       *DepartmentNotice
	Preview                      *ComparePreview
	Story                        *ColourStory
}

// Facts leaves out a count that is zero: a department with no brands says nothing about them.
func (h *DepartmentHead) Facts(ctx context.Context) []components.Stat {
	var out []components.Stat
	if h.Products > 0 {
		out = append(out, components.Stat{Label: i18n.T(ctx, i18n.KeySlideItems), Value: StatCountOf(ctx, i18n.KeyUnitItems, h.Products)})
	}
	if h.Categories > 0 {
		out = append(out, components.Stat{Label: i18n.T(ctx, i18n.KeySlideCategories), Value: StatCountOf(ctx, i18n.KeyUnitCategories, h.Categories)})
	}
	if h.Brands > 0 {
		out = append(out, components.Stat{Label: i18n.T(ctx, i18n.KeyDeptBrands), Value: StatCountOf(ctx, i18n.KeyUnitBrands, h.Brands)})
	}
	return out
}

// EditorialSlot is the one piece of editorial under the head.
type EditorialSlot int

const (
	SlotNone EditorialSlot = iota
	SlotCompare
	SlotStory
)

// Slot is the first that holds: the comparison, then the colour story.
func (h *DepartmentHead) Slot() EditorialSlot {
	switch {
	case h.Preview != nil:
		return SlotCompare
	case h.Story != nil:
		return SlotStory
	default:
		return SlotNone
	}
}

// DepartmentNotice is the running campaign that features products of the department.
type DepartmentNotice struct {
	Title  string
	Href   string
	Ends   components.Stat
	Period *components.PeriodSpec
}

// ComparePreview is the comparison a comparable department offers: the same table as /compare,
// with only the specifications the products share.
type ComparePreview struct {
	Category string
	Table    CompareView
}

// NewComparePreview keeps the first previewProducts products and the rows at least two of them
// state, at most previewMaxRows. It is false when fewer than previewMinRows rows remain, which one
// product never reaches: a row needs two products stating it.
func NewComparePreview(category string, v CompareView) (ComparePreview, bool) {
	products := v.Products[:min(len(v.Products), previewProducts)]
	var rows []CompareRow
	for _, r := range v.Rows {
		values := r.Values[:min(len(r.Values), len(products))]
		stated := 0
		for _, value := range values {
			if value != "" {
				stated++
			}
		}
		if stated >= 2 {
			rows = append(rows, CompareRow{Label: r.Label, Values: values, SharedBy: stated})
		}
	}
	if len(rows) < previewMinRows {
		return ComparePreview{}, false
	}
	rows = rows[:min(len(rows), previewMaxRows)]
	return ComparePreview{Category: category, Table: CompareView{Products: products, Rows: rows}}, true
}

// ColourStory is one product shown by its colours, each with its own photograph.
type ColourStory struct {
	Slug, Name, Summary, Brand string
	Price                      ProductTile
	Plates                     []ColourPlate
}

type ColourPlate struct {
	Colour, Swatch string
	Image          Photo
}

// Title names the colours, as the story's own words.
func (s *ColourStory) Title(ctx context.Context) string {
	names := make([]string, len(s.Plates))
	for i, p := range s.Plates {
		names[i] = p.Colour
	}
	return strings.Join(names, i18n.T(ctx, i18n.KeyDeptColourJoin))
}

// SelfHref is the comparison page of exactly these products.
func (v CompareView) SelfHref() string { return v.RemoveHref("") }
