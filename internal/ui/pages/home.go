package pages

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
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

type ProductRow struct {
	Title string
	Fact  string
	Href  string
	Tiles []ProductTile
}

type DepartmentBand struct {
	Name  string
	Fact  string
	Href  string
	Tone  Tone
	Photo Photo
	Tiles []ProductTile
}

type HomeView struct {
	Slides            []HeroSlide
	Categories        []HomeCategory
	Row               ProductRow
	Band              *DepartmentBand
	FreeDeliveryCents int64
	LowestFeeCents    int64
	// PickupOffered gates the shipping strip's claim of store pickup.
	PickupOffered bool
}

func (v *HomeView) FreeDelivery() string { return FreeDeliveryText(v.FreeDeliveryCents) }

func (v *HomeView) ShippingBodyKey() i18n.Key {
	if v.PickupOffered {
		return i18n.KeyTrustShippingBody
	}
	return i18n.KeyTrustShippingHomeBody
}

func (v *HomeView) LowestFee() string { return twd(v.LowestFeeCents) }

func (v *HomeView) PromoHref() string {
	const slug = "books-stationery"
	for _, c := range v.Categories {
		if c.Slug == slug {
			return "/c/" + slug
		}
	}
	return "/search"
}
