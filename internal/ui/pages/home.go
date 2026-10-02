package pages

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// HomeMeta is the chrome view model for the storefront home page. It has no
// title of its own, so the tab reads as the shop's name alone.
func HomeMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Description: describeDepartments(ctx, i18n.KeyHomeDescription)}
}

// departments is the shop's root categories as one phrase in the reader's
// language, or "" when there are none. It reads the header's category row,
// the same RootCategories rows the home tiles show, so a sentence naming them
// cannot drift from the catalogue.
func departments(ctx context.Context) string {
	items := layouts.TopNavFrom(ctx)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return joinList(ctx, names)
}

// describeDepartments interpolates the departments into k, or is "" when there
// are none: a description left out is better than one naming nothing.
func describeDepartments(ctx context.Context, k i18n.Key) string {
	list := departments(ctx)
	if list == "" {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, k), list)
}

// joinList joins names the way the reader's language lists things: English
// puts "and" before the last, Chinese uses 、 throughout.
func joinList(ctx context.Context, names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	last := len(names) - 1
	return strings.Join(names[:last], i18n.T(ctx, i18n.KeyListSeparator)) +
		i18n.T(ctx, i18n.KeyListLastSeparator) + names[last]
}

// HomeCategory is a department card: its own tone and photograph.
type HomeCategory struct {
	Slug  string
	Name  string
	Tone  Tone
	Photo Photo
}

// ProductRow is a heading, one muted line and a row of tiles: the running
// campaign's products, or the newest of the shop when none runs.
type ProductRow struct {
	Title string
	Fact  string
	Href  string
	Tiles []ProductTile
}

// DepartmentBand is the one department the home page features on its own
// ground: its photograph large beside three of its products.
type DepartmentBand struct {
	Name  string
	Fact  string
	Href  string
	Tone  Tone
	Photo Photo
	Tiles []ProductTile
}

// HomeView is everything the home page renders.
type HomeView struct {
	Slides     []HeroSlide
	Categories []HomeCategory
	Row        ProductRow
	// Band is nil when no department has a photograph and three products.
	Band              *DepartmentBand
	FreeDeliveryCents int64
	LowestFeeCents    int64
	// PickupOffered is whether checkout offers store pickup, which the shipping
	// strip may only claim where it does.
	PickupOffered bool
}

// FreeDelivery is the threshold the trust strip states, or "" for none.
func (v *HomeView) FreeDelivery() string { return FreeDeliveryText(v.FreeDeliveryCents) }

// ShippingBodyKey is the strip's sentence for the methods checkout offers.
func (v *HomeView) ShippingBodyKey() i18n.Key {
	if v.PickupOffered {
		return i18n.KeyTrustShippingBody
	}
	return i18n.KeyTrustShippingHomeBody
}

// LowestFee is the floor the trust body states.
func (v *HomeView) LowestFee() string { return twd(v.LowestFeeCents) }

// PromoHref is where the promotional band leads: the books and stationery
// department when the shop has one, else search.
func (v *HomeView) PromoHref() string {
	const slug = "books-stationery"
	for _, c := range v.Categories {
		if c.Slug == slug {
			return "/c/" + slug
		}
	}
	return "/search"
}
