package pages

import (
	"context"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

const (
	previewProducts = 3
	previewMinRows  = 3
	previewMaxRows  = 6
	// StoryColours is how many colours a colour story shows, and the fewest it needs.
	StoryColours = 3
)

// DepartmentHead is what a department page says under its band. Notice, Preview and Story
// are nil where the department has nothing to say.
type DepartmentHead struct {
	Notice  *DepartmentNotice
	Preview *ComparePreview
	Story   *ColourStory
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
	Title string
	Href  string
	End   CampaignEnd
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
