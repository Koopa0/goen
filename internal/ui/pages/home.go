package pages

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func HomeMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Description: describeDepartments(ctx, i18n.KeyHomeDescription)}
}

// departments reads the header's category row, the same rows the home tiles
// show, so a sentence naming them cannot drift from the catalogue.
func departments(ctx context.Context) string {
	items := layouts.TopNavFrom(ctx)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return joinList(ctx, names)
}

// A description left out is better than one naming nothing.
func describeDepartments(ctx context.Context, k i18n.Key) string {
	list := departments(ctx)
	if list == "" {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, k), list)
}

// English puts "and" before the last; Chinese uses 、 throughout.
func joinList(ctx context.Context, names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	last := len(names) - 1
	return strings.Join(names[:last], i18n.T(ctx, i18n.KeyListSeparator)) +
		i18n.T(ctx, i18n.KeyListLastSeparator) + names[last]
}

type HomeCategory struct {
	Slug  string
	Name  string
	Tone  Tone
	Photo Photo
}

// Initial is the first character of the name, which a department without a
// photograph shows on its tone.
func (c HomeCategory) Initial() string {
	r, _ := utf8.DecodeRuneInString(c.Name)
	if r == utf8.RuneError {
		return ""
	}
	return string(r)
}

// ProductRow is the home page's one row of products: a running campaign's, or
// the newest of the shop when none is running.
type ProductRow struct {
	Title    string
	Href     string
	Tiles    []ProductTile
	Campaign *RowCampaign
}

// RowCampaign is what a campaign's row says about the campaign.
type RowCampaign struct {
	Tone  Tone
	Items int64
	// Facts stand under the heading when the row has no campaign card.
	Facts []components.Stat
	// CardFacts are what is left and when it ends, for the campaign card.
	CardFacts []components.Stat
	Period    *components.PeriodSpec
}

// leadPhotoWidth is the narrowest first photograph that holds up at the lead
// tile's size; a photograph of unknown width counts as narrower.
const (
	leadPhotoWidth = 1200
	leadTiles      = 4
)

// Shelf is the tiles as drawn: under the hero, and with the lead marked.
func (r ProductRow) Shelf() []ProductTile {
	tiles := UnderLeadEager(r.Tiles)
	if r.HasLead() {
		tiles[0].Lead = true
	}
	return tiles
}

// HasLead reports whether the row puts its first product in a 2×2 tile on the
// campaign's tone, with the campaign card as the eighth cell.
func (r ProductRow) HasLead() bool {
	return r.Campaign != nil && len(r.Tiles) >= leadTiles && r.Tiles[0].ImageWidth >= leadPhotoWidth
}

type DepartmentBand struct {
	Name string
	// Items is the number of products the department holds.
	Items int64
	Fact  string
	Href  string
	Tone  Tone
	Tiles []ProductTile
}

type HomeView struct {
	Slides     []HeroSlide
	Categories []HomeCategory
	Row        ProductRow
	Band       *DepartmentBand
	Rules      ShopRules
}

// ShowsDirectory reports whether the department list is drawn. With one
// department the band is the department, unless no band is drawn.
func (v *HomeView) ShowsDirectory() bool {
	return len(v.Categories) > 1 || len(v.Categories) == 1 && v.Band == nil
}
