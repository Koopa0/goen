package cart

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/widget"
)

type View struct {
	Lines         []Line
	SubtotalCents int64
	ItemCount     int64
	MixedTaxTypes bool

	ReorderAdded    int
	ReorderSkipped  int
	ReorderAdjusted bool
	Notice          string
	ContinueURL     string

	FreeDelivery FreeDelivery
	NoDelivery   bool

	// HoldMinutes is how long placing the order holds the stock, and PayStartMinutes how long the customer then has
	// to start paying: the windows the cart enforces.
	HoldMinutes     int64
	PayStartMinutes int64
}

type FreeDeliveryKind string

const (
	// FreeDeliveryUnstated is silence: the methods on offer disagree, so no sentence is true for every destination.
	FreeDeliveryUnstated FreeDeliveryKind = ""
	FreeDeliveryShort    FreeDeliveryKind = "short"
	FreeDeliveryReached  FreeDeliveryKind = "reached"
)

type FreeDelivery struct {
	Kind           FreeDeliveryKind
	ShortfallCents int64
	// ThresholdCents is zero where no method names an amount it turns free at.
	ThresholdCents int64
	// SurchargeZones are the zones some offered method charges extra to even once delivery is free.
	SurchargeZones []string
}

// Word is what the cart says once delivery is free, with the zones it does not cover.
func (f FreeDelivery) Word(ctx context.Context) string {
	if len(f.SurchargeZones) == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyFreeShippingExceptZones),
		strings.Join(f.SurchargeZones, i18n.T(ctx, i18n.KeyListSeparator)))
}

// Stat is the fact that tells where the cart stands against free delivery; it is absent where the methods on offer disagree.
func (f FreeDelivery) Stat(ctx context.Context) widget.Stat {
	switch f.Kind {
	case FreeDeliveryShort:
		return widget.Stat{
			Label: i18n.T(ctx, i18n.KeyCartFactToFree),
			Value: widget.StatMoney(f.ShortfallCents),
			Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingFreeOver), twd(f.ThresholdCents)),
		}
	case FreeDeliveryReached:
		s := widget.Stat{Label: i18n.T(ctx, i18n.KeyShippingFee), Value: widget.StatWord(f.Word(ctx))}
		if f.ThresholdCents > 0 {
			s.Note = fmt.Sprintf(i18n.T(ctx, i18n.KeyCartFactOver), twd(f.ThresholdCents))
		}
		return s
	default:
		return widget.Stat{}
	}
}

// Facts are the cart's totals as they stand: a sold-out line is in neither the count nor the subtotal.
func (v View) Facts(ctx context.Context) []widget.Stat {
	return []widget.Stat{
		{Label: i18n.T(ctx, i18n.KeyCartFactItems), Value: widget.StatCount(v.ItemCount, i18n.T(ctx, i18n.KeyFactUnitItems))},
		{Label: i18n.T(ctx, i18n.KeySubtotal), Value: widget.StatMoney(v.SubtotalCents)},
		v.FreeDelivery.Stat(ctx),
	}
}

// StockNotice says what stands between the shopper and checkout; a sold-out line outranks a short one.
func (v View) StockNotice(ctx context.Context) string {
	switch {
	case v.HasSoldOut():
		return i18n.T(ctx, i18n.KeyCartSoldOut)
	case v.Blocked():
		return i18n.T(ctx, i18n.KeyCartStockShort)
	default:
		return ""
	}
}

func (v View) HasSoldOut() bool {
	return slices.ContainsFunc(v.Lines, func(l Line) bool { return l.Unavailable })
}

func (v View) HasNotice() bool { return v.Notice != "" }

// HoldNote is the line under the checkout button that says how long placing the order holds the stock.
func (v View) HoldNote(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyCartHold, v.HoldMinutes, strconv.FormatInt(v.HoldMinutes, 10))
}

func (v View) FromReorder() bool { return v.ReorderAdded > 0 || v.ReorderSkipped > 0 }

func (v View) ReorderText(ctx context.Context) string {
	switch {
	case v.ReorderAdjusted && v.ReorderSkipped > 0:
		return i18n.Count(ctx, i18n.KeyReorderAdjustedPartial, int64(v.ReorderSkipped), v.ReorderSkipped)
	case v.ReorderAdjusted:
		return i18n.T(ctx, i18n.KeyReorderAdjusted)
	case v.ReorderSkipped == 0:
		return i18n.Count(ctx, i18n.KeyReorderAll, int64(v.ReorderAdded), v.ReorderAdded)
	case v.ReorderAdded == 0:
		return i18n.T(ctx, i18n.KeyReorderNone)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderPartial), v.ReorderAdded, v.ReorderSkipped)
	}
}

func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCart)}
}

func (v View) Empty() bool { return len(v.Lines) == 0 }

func (v View) Subtotal() string { return twd(v.SubtotalCents) }

func (v View) Blocked() bool {
	for i := range v.Lines {
		if v.Lines[i].Unavailable || v.Lines[i].Short {
			return true
		}
	}
	return false
}

func (v View) CanCheckout() bool { return !v.Empty() && !v.Blocked() && !v.MixedTaxTypes }

type ShippingChoice struct {
	VersionID       string
	Code            string
	DestinationKind string
	Name            string
	Carrier         string
	FeeCents        int64
	Free            bool
	// FreeOverCents is zero for a method that is never free and for one that costs nothing at any subtotal.
	FreeOverCents int64
	// SurchargeZones are the zones this method charges extra to, which free delivery does not waive.
	SurchargeZones []string
}

func (c ShippingChoice) Fee() string { return twd(c.FeeCents) }

// cartLineSwap names what one quantity update changes: that line's text and price,
// the summary, the item count, the notices and the header's cart link. The stepper
// in use and the other lines are never replaced.
func cartLineSwap(variantID string) string {
	return "#line-body-" + variantID + ",#line-money-" + variantID +
		",#cart-summary,#cart-notices,#cart-link,#cart-count:innerHTML"
}
