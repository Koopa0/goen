// Package tile is a product as one card in a grid of products, drawn the same
// on every page that lists products; it holds the tile's view, its card, grid
// cell and price line with the struck compare-at price, and which tiles load at
// once; it does not hold whether a price is struck (campaign.OnSale, which the
// caller applies), the pages that list tiles (catalog, campaign, home,
// wishlist) or page elements without a product (ui/widget).
package tile

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

type Tile struct {
	// Eager and Priority are set by FirstRowEager for the tiles shown without
	// scrolling, whose photograph is the page's largest paint.
	Eager      bool
	Priority   bool
	Slug       string
	Name       string
	Summary    string
	Brand      string
	PriceCents int64
	// PriceVaries marks PriceCents as the cheapest of several: a "from" price.
	PriceVaries  bool
	CompareCents int64 // 0 when the product is not on sale
	// OnSale strikes CompareCents beside the price, as campaign.OnSale decides.
	OnSale      bool
	Rating      float64
	RatingCount int64
	// InStock is stock above safety_stock, not stock_quantity > 0.
	InStock     bool
	ImageURL    string // "" when the product has no usable image
	ImageSrcset string
	ImageAlt    string
	ImageWidth  int32 // 0 when the stored media has no declared width
	ImageHeight int32 // 0 when the stored media has no declared height
	// Colours are the swatches of the product's colour option, the first option all of whose
	// values are colours, as #rrggbb; empty when it has none.
	Colours []string
	// Set only where somebody is choosing between candidates (listing, search); not a
	// shop window, a promotional list or a wishlist.
	Comparable bool
	// Lead marks the 2×2 tile of the home row, whose photograph is larger than a card's.
	Lead bool
	// Highlights are the first specifications a comparable product lists, for the line under its name.
	Highlights []string
}

// Sizes is the width the photograph is laid out at, for the browser's choice of file.
func (t *Tile) Sizes() string {
	if t.Lead {
		return "(min-width: 1344px) 596px, (min-width: 1024px) calc(50vw - 44px), calc(100vw - 48px)"
	}
	return "(min-width: 1344px) 286px, (min-width: 1024px) calc((100vw - 136px) / 4), (min-width: 768px) calc((100vw - 96px) / 3), calc((100vw - 48px) / 2)"
}

func AnyComparable(tiles []Tile) bool {
	return slices.ContainsFunc(tiles, func(t Tile) bool { return t.Comparable })
}

func (t *Tile) CompareLabel(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyCompareAddNamed), t.Name)
}

func (t *Tile) SoldOut() bool { return !t.InStock }

// maxDots is how many colours a card draws; the rest are counted.
const maxDots = 4

// HasChoiceOfColour is false for a single colour: that is not a choice to show.
func (t *Tile) HasChoiceOfColour() bool { return len(t.Colours) >= 2 }

func (t *Tile) Dots() []string { return t.Colours[:min(len(t.Colours), maxDots)] }

func (t *Tile) MoreColours() int { return max(len(t.Colours)-maxDots, 0) }

func (t *Tile) HasImage() bool { return t.ImageURL != "" }

func (t *Tile) HasImageDimensions() bool { return t.ImageWidth > 0 && t.ImageHeight > 0 }

func (t *Tile) ImageWidthText() string { return strconv.FormatInt(int64(t.ImageWidth), 10) }

func (t *Tile) ImageHeightText() string { return strconv.FormatInt(int64(t.ImageHeight), 10) }

func (t *Tile) Price() string { return money.TWD(t.PriceCents) }

func (t *Tile) Compare() string { return money.TWD(t.CompareCents) }

// The first two rows at the widest grid, which is also the first four of a
// phone's: a second row starts inside a 900px-tall window, and a lazy photograph
// there appears after the page has painted.
const eagerTiles = 8

// FirstRowEager marks the leading tiles eager, the first at high priority. The
// caller's slice is not changed.
func FirstRowEager(tiles []Tile) []Tile {
	return eagerLeading(tiles, true)
}

// UnderLeadEager is FirstRowEager for tiles under a hero photograph:
// that is the page's one high-priority image, and a second would split bandwidth.
func UnderLeadEager(tiles []Tile) []Tile {
	return eagerLeading(tiles, false)
}

func eagerLeading(tiles []Tile, lead bool) []Tile {
	out := make([]Tile, len(tiles))
	copy(out, tiles)
	for i := range min(eagerTiles, len(out)) {
		out[i].Eager = true
		out[i].Priority = lead && i == 0
	}
	return out
}
