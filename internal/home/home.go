package home

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/policy"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/widget"
)

func Meta(ctx context.Context) frame.Page {
	return frame.Page{Description: frame.DescribeDepartments(ctx, i18n.KeyHomeDescription)}
}

// Choice is what a visitor picked on the page, by slug, carried in the
// address so a choice can be linked to and survives a reload: a sub-category
// of the featured shelf and a department of the showcase. "" is the default.
type Choice struct {
	Shelf string
	More  string
}

// Href is the home page's address with this choice, the link a pill or a tab
// falls back to.
func (c Choice) Href() string {
	q := url.Values{}
	if c.Shelf != "" {
		q.Set("shelf", c.Shelf)
	}
	if c.More != "" {
		q.Set("more", c.More)
	}
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
}

// Category is a department or a sub-category, with the active products of its
// whole subtree.
type Category struct {
	Slug     string
	Name     string
	Products int64
}

// Href is the category's own page.
func (c Category) Href() string { return "/c/" + c.Slug }

type View struct {
	AccountErased bool
	Choice        Choice
	// Products is how many products the shop lists now.
	Products int64
	// Featured is the department holding the most products; nil in a shop
	// with none.
	Featured *Category
	// Lifestyle is the hero's second tile; nil when there is nothing to show.
	Lifestyle *hero.Slide
	Shelf     *Shelf
	// Stage is the featured department's two highest-priced products that can
	// be bought; empty unless there are two.
	Stage []tile.Tile
	Rail  Rail
	More  *More
	Rules policy.Rules
}

// Shelf is the featured department's products, all of them or one
// sub-category's.
type Shelf struct {
	Department Category
	// Filters are the department's sub-categories that hold products.
	Filters []Category
	// Chosen is the sub-category shown, by slug; "" is all of the department.
	Chosen string
	Tiles  []tile.Tile
	// Compares is the shown category's answer to whether its products are put
	// side by side: where it is, each card carries the compare toggle.
	Compares bool
}

// Rail is the products under the ceiling, cheapest first.
type Rail struct {
	BelowCents int64
	Tiles      []tile.Tile
}

// Below is the ceiling as a price.
func (r Rail) Below() string { return money.TWD(r.BelowCents) }

// Shelf is the rail's tiles as drawn, every photograph loaded with the page:
// a card past the rail's edge lies beside the window in a scroller, where a
// lazy photograph may not be asked for until it is swiped to, and the card
// would arrive empty.
func (r Rail) Shelf() []tile.Tile {
	out := make([]tile.Tile, len(r.Tiles))
	copy(out, r.Tiles)
	for i := range out {
		out[i].Eager, out[i].Priority = true, false
	}
	return out
}

// More is the showcase of the other departments, one at a time.
type More struct {
	Tabs   []Category
	Chosen Category
	Photo  widget.Photo
	// Subs are the chosen department's sub-categories, by name.
	Subs  []string
	Tiles []tile.Tile
}

// stageMeta is the line over a stage product's name: its brand and the
// warranty the shop states, whichever it has.
func stageMeta(ctx context.Context, t *tile.Tile) string {
	var parts []string
	if t.Brand != "" {
		parts = append(parts, t.Brand)
	}
	if t.WarrantyMonths > 0 {
		parts = append(parts, fmt.Sprintf(i18n.T(ctx, i18n.KeyPDPWarranty), strconv.Itoa(int(t.WarrantyMonths))))
	}
	return strings.Join(parts, " · ")
}

// IndexLink is one way in from the quick index.
type IndexLink struct {
	Label string
	Href  string
}

// maxIndex is how many links the quick index holds, two by two.
const maxIndex = 4

// Index is the quick index: the featured department's sub-categories, then
// the rail where it has products and the deals where any are on offer, which
// take the last places.
func (v *View) Index(ctx context.Context) []IndexLink {
	var tail []IndexLink
	if len(v.Rail.Tiles) > 0 {
		tail = append(tail, IndexLink{Label: fmt.Sprintf(i18n.T(ctx, i18n.KeyHomeIndexUnder), v.Rail.Below()), Href: "#under"})
	}
	if frame.HasDeals(ctx) {
		tail = append(tail, IndexLink{Label: i18n.T(ctx, i18n.KeyDeals), Href: "/deals"})
	}
	var out []IndexLink
	if v.Shelf != nil {
		for _, c := range v.Shelf.Filters {
			if len(out)+len(tail) == maxIndex {
				break
			}
			out = append(out, IndexLink{Label: c.Name, Href: c.Href()})
		}
	}
	return append(out, tail...)
}

// IndexHead is the quick index's heading, with the free-delivery threshold
// where every method turns free at one.
func (v *View) IndexHead(ctx context.Context) string {
	if v.Rules.FreeDeliveryCents > 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyHomeIndexFree), money.TWD(v.Rules.FreeDeliveryCents))
	}
	return i18n.T(ctx, i18n.KeyHomeIndex)
}

// Facts are the rules the quick index restates, each where the shop states it.
func (v *View) Facts(ctx context.Context) []string {
	var out []string
	if v.Rules.LowestFeeCents > 0 {
		out = append(out, fmt.Sprintf(i18n.T(ctx, i18n.KeyHomeFactFee), money.TWD(v.Rules.LowestFeeCents)))
	}
	if v.Rules.RescissionDays > 0 {
		out = append(out, fmt.Sprintf(i18n.T(ctx, i18n.KeyHomeFactRescission), v.Rules.RescissionDays))
	}
	if v.Rules.ReturnDays > 0 {
		out = append(out, i18n.Count(ctx, i18n.KeyHomeFactReturn, v.Rules.ReturnDays, v.Rules.ReturnDays))
	}
	return out
}

// HeroPhoto is the bundled photograph under the dark tile.
func HeroPhoto() widget.Photo {
	return bundled(assets.HeroTechImage, assets.HeroTechImage800, assets.HeroTechImage720)
}

// LifestylePhoto is a slide's own photograph, or the bundled one for a slide
// without.
func LifestylePhoto(s *hero.Slide) widget.Photo {
	if s.Photo.Shown() {
		return s.Photo
	}
	return bundled(assets.HeroLifestyleImage, assets.HeroLifestyleImage800, assets.HeroLifestyleImage720)
}

// bundled is a bundled hero photograph with its renditions, decorative: the
// words on it are the tile's.
func bundled(full, w800, w720 string) widget.Photo {
	return widget.Photo{
		URL: assets.URL(full),
		Srcset: strings.Join([]string{
			assets.URL(w720) + " 720w",
			assets.URL(w800) + " 800w",
			assets.URL(full) + " 1600w",
		}, ", "),
	}
}

// Meter is where the visitor's cart stands against free delivery, in the
// sentence the header's rail says it in, with the bar's value and its end.
// It says nothing where the cart's page says nothing.
type Meter struct {
	Sentence string
	Href     string
	// Value and Max are the bar, in cents; Max is zero where no bar is drawn.
	Value, Max int64
	Free       bool
}

// Shown reports whether the meter has anything to say.
func (m Meter) Shown() bool { return m.Sentence != "" }

// Figure is the bar in words, the subtotal over the amount that makes every
// method free; "" once delivery is free, when there is nothing left to count.
func (m Meter) Figure() string {
	if m.Max == 0 || m.Free {
		return ""
	}
	return money.TWD(m.Value) + " / " + money.TWD(m.Max)
}

// CartMeter reads the meter off the cart the middleware put on the request,
// in the same cases the header's rail does (frame's deliveryWord): a cart
// short of free delivery, a cart there, and for any other visitor the shop's
// stated threshold.
func CartMeter(ctx context.Context) Meter {
	c := frame.CartFrom(ctx)
	switch {
	case c.Units > 0 && c.Delivery.ShortCents > 0:
		return Meter{
			Sentence: i18n.T(ctx, i18n.KeyCartFactToFree) + " " + money.TWD(c.Delivery.ShortCents), Href: "/cart",
			Value: c.SubtotalCents, Max: c.SubtotalCents + c.Delivery.ShortCents,
		}
	case c.Units > 0 && c.Delivery.Free && len(c.Delivery.Zones) > 0:
		return Meter{
			Sentence: fmt.Sprintf(i18n.T(ctx, i18n.KeyFreeDeliveryReachedExcept),
				strings.Join(c.Delivery.Zones, i18n.T(ctx, i18n.KeyListSeparator))),
			Href: "/cart", Free: true, Value: c.SubtotalCents, Max: c.SubtotalCents,
		}
	case c.Units > 0 && c.Delivery.Free:
		return Meter{Sentence: i18n.T(ctx, i18n.KeyFreeDeliveryReached), Href: "/cart", Free: true, Value: c.SubtotalCents, Max: c.SubtotalCents}
	case c.Units > 0:
		return Meter{}
	case frame.FreeDeliveryOver(ctx) > 0:
		over := frame.FreeDeliveryOver(ctx)
		return Meter{
			Sentence: fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingFreeOver), money.TWD(over)), Href: "/shipping",
			Max: over,
		}
	}
	return Meter{}
}
