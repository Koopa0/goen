package home

import (
	"context"
	"slices"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

func Meta(ctx context.Context) frame.Page {
	return frame.Page{Description: frame.DescribeDepartments(ctx, i18n.KeyHomeDescription)}
}

type Category struct {
	Slug  string
	Name  string
	Tone  tone.Tone
	Photo widget.Photo
}

// Initial is the first character of the name, which a department without a
// photograph shows on its tone.
func (c Category) Initial() string {
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
	Tiles    []tile.Tile
	Campaign *RowCampaign
}

// RowCampaign is what a campaign's row says about the campaign.
type RowCampaign struct {
	Items int64
	End   campaign.End
}

// Shelf is the tiles as drawn, under the hero.
func (r ProductRow) Shelf() []tile.Tile {
	return tile.UnderLeadEager(r.Tiles)
}

type DepartmentBand struct {
	Name string
	// Items is the number of products the department holds.
	Items int64
	Fact  string
	Href  string
	Tone  tone.Tone
	Tiles []tile.Tile
}

type View struct {
	AccountErased bool
	Slides        []hero.Slide
	Categories    []Category
	Row           ProductRow
	Band          *DepartmentBand
	Rules         policy.Rules
}

// ShowsRow reports whether the product row is drawn. A campaign's products
// are said once: where its slide carries them, the row under the hero is not.
func (v *View) ShowsRow() bool { return !heroCarries(v.Slides, v.Row) }

// heroCarries reports that a slide of the hero draws the row's campaign and its products.
func heroCarries(slides []hero.Slide, row ProductRow) bool {
	return row.Campaign != nil && slices.ContainsFunc(slides, func(s hero.Slide) bool {
		return s.Source == hero.SourceCampaign && s.CTA.Href == row.Href && len(s.Tiles) > 0
	})
}

// ShowsDirectory reports whether the department list is drawn. With one
// department the band is the department, unless no band is drawn.
func (v *View) ShowsDirectory() bool {
	return len(v.Categories) > 1 || len(v.Categories) == 1 && v.Band == nil
}
