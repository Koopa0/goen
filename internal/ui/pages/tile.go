package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// ProductTile is one product card — the same card on the home grid and in a
// category listing, because they render the same component. It carries the
// cheapest BUYABLE variant's price, a rating, and the primary image. Handlers
// fill these from a store; the template only displays what it is given.
type ProductTile struct {
	Slug         string
	Name         string
	Summary      string
	Brand        string
	PriceCents   int64
	CompareCents int64 // 0 when the product is not on sale
	Rating       float64
	RatingCount  int64
	// InStock is whether any active variant can actually be BOUGHT — stock above
	// safety_stock, which is the floor record_inventory_movement enforces on a
	// sale or a hold. A variant sitting at the floor has stock and cannot be
	// sold, so this is not "stock_quantity > 0".
	InStock     bool
	ImageURL    string // "" when the product has no usable image
	ImageSrcset string
	ImageAlt    string
	ImageWidth  int32 // 0 when the stored media has no declared width
	ImageHeight int32 // 0 when the stored media has no declared height
}

// OnSale reports whether the tile shows a struck-through compare-at price. A
// sold-out product does not advertise its discount: the saving is not available
// to anyone, and the tile has one flag position to spend.
func (t ProductTile) OnSale() bool { return t.InStock && t.CompareCents > t.PriceCents }

// SoldOut reports whether nothing on this product can be bought.
func (t ProductTile) SoldOut() bool { return !t.InStock }

// HasImage reports whether the tile has a product image to show.
func (t ProductTile) HasImage() bool { return t.ImageURL != "" }

// HasImageDimensions reports whether both intrinsic image dimensions are
// available. They are emitted together so the browser never receives a
// misleading partial aspect ratio.
func (t ProductTile) HasImageDimensions() bool { return t.ImageWidth > 0 && t.ImageHeight > 0 }

// ImageWidthText and ImageHeightText are the intrinsic dimensions as HTML
// attribute values.
func (t ProductTile) ImageWidthText() string { return strconv.FormatInt(int64(t.ImageWidth), 10) }

func (t ProductTile) ImageHeightText() string { return strconv.FormatInt(int64(t.ImageHeight), 10) }

// Price is the display price, e.g. "NT$33,980".
func (t ProductTile) Price() string { return twd(t.PriceCents) }

// Compare is the struck-through original price, shown only when OnSale.
func (t ProductTile) Compare() string { return twd(t.CompareCents) }

// RatingText is the average rating to one decimal, e.g. "4.7".
func (t ProductTile) RatingText() string { return strconv.FormatFloat(t.Rating, 'f', 1, 64) }

// HasReviews reports whether the tile has any ratings to show.
func (t ProductTile) HasReviews() bool { return t.RatingCount > 0 }

// ReviewCountText is the number of ratings as text, e.g. "3".
func (t ProductTile) ReviewCountText() string { return strconv.FormatInt(t.RatingCount, 10) }

// RatingLabel is the rating as one sentence for assistive technology. The tile
// hides the star, the score and the count from the accessibility tree and
// announces this instead, so a screen reader hears "評分 4.7 分,共 3 則評價"
// rather than a bare star glyph followed by two loose numbers.
func (t ProductTile) RatingLabel(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyRatingSummary), t.RatingText(), t.ReviewCountText())
}

// TWD is twd for callers outside this package.
//
// Exported reluctantly and for one reason: a handler that has to put money in a
// flash message would otherwise format it itself, and two money formatters is
// how one of them comes to be missing the thousands separator.
func TWD(cents int64) string { return twd(cents) }

// twd formats an amount in cents as New Taiwan dollars: 3398000 -> "NT$33,980".
// TWD is quoted as a whole number, so the cents are folded into the dollar
// amount and grouped in thousands.
func twd(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-NT$" + b.String()
	}
	return "NT$" + b.String()
}
