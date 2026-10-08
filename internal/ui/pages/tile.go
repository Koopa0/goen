package pages

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

type ProductTile struct {
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
	// InCampaign reports that a listed campaign (one with a deal) features the product; only then is CompareCents struck.
	InCampaign  bool
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
func (t *ProductTile) Sizes() string {
	if t.Lead {
		return "(min-width: 1344px) 596px, (min-width: 1024px) calc(50vw - 44px), calc(100vw - 48px)"
	}
	return "(min-width: 1344px) 286px, (min-width: 1024px) calc((100vw - 136px) / 4), (min-width: 768px) calc((100vw - 96px) / 3), calc((100vw - 48px) / 2)"
}

func AnyComparable(tiles []ProductTile) bool {
	return slices.ContainsFunc(tiles, func(t ProductTile) bool { return t.Comparable })
}

func (t *ProductTile) CompareLabel(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyCompareAddNamed), t.Name)
}

func (t *ProductTile) OnSale() bool {
	return t.InStock && t.InCampaign && t.CompareCents > t.PriceCents
}

func (t *ProductTile) SoldOut() bool { return !t.InStock }

// maxDots is how many colours a card draws; the rest are counted.
const maxDots = 4

// HasChoiceOfColour is false for a single colour: that is not a choice to show.
func (t *ProductTile) HasChoiceOfColour() bool { return len(t.Colours) >= 2 }

func (t *ProductTile) Dots() []string { return t.Colours[:min(len(t.Colours), maxDots)] }

func (t *ProductTile) MoreColours() int { return max(len(t.Colours)-maxDots, 0) }

func (t *ProductTile) HasImage() bool { return t.ImageURL != "" }

func (t *ProductTile) HasImageDimensions() bool { return t.ImageWidth > 0 && t.ImageHeight > 0 }

func (t *ProductTile) ImageWidthText() string { return strconv.FormatInt(int64(t.ImageWidth), 10) }

func (t *ProductTile) ImageHeightText() string { return strconv.FormatInt(int64(t.ImageHeight), 10) }

func (t *ProductTile) Price() string { return twd(t.PriceCents) }

func (t *ProductTile) Compare() string { return twd(t.CompareCents) }

func (t *ProductTile) RatingText() string { return strconv.FormatFloat(t.Rating, 'f', 1, 64) }

func TWD(cents int64) string { return twd(cents) }

func twd(cents int64) string { return money.TWD(cents) }

// The first two rows at the widest grid, which is also the first four of a
// phone's: a second row starts inside a 900px-tall window, and a lazy photograph
// there appears after the page has painted.
const eagerTiles = 8

// FirstRowEager marks the leading tiles eager, the first at high priority. The
// caller's slice is not changed.
func FirstRowEager(tiles []ProductTile) []ProductTile {
	return eagerLeading(tiles, true)
}

// UnderLeadEager is FirstRowEager for tiles under a hero photograph:
// that is the page's one high-priority image, and a second would split bandwidth.
func UnderLeadEager(tiles []ProductTile) []ProductTile {
	return eagerLeading(tiles, false)
}

func eagerLeading(tiles []ProductTile, lead bool) []ProductTile {
	out := make([]ProductTile, len(tiles))
	copy(out, tiles)
	for i := range min(eagerTiles, len(out)) {
		out[i].Eager = true
		out[i].Priority = lead && i == 0
	}
	return out
}
