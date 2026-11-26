package cart

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
)

type View struct {
	Lines           []Line
	SubtotalCents   int64
	ItemCount       int64
	MixedTaxTypes   bool
	ReorderAdjusted bool
	NoDelivery      bool
	// CheckoutEmpty is set only on a cart with no lines, which /checkout turned away.
	CheckoutEmpty bool
	// SavedProducts is whether the signed-in customer has a wishlist worth opening.
	SavedProducts bool

	ReorderAdded   int32
	ReorderSkipped int32
	// ReorderLeftOut names the lines behind ReorderSkipped; it is empty for a browser
	// that may not see the order it came from.
	ReorderLeftOut []SkippedLine
	Notice         widget.Result
	ContinueURL    string

	FreeDelivery FreeDelivery

	// PayStartMinutes is how long the customer has, from placing the order, to start paying.
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

// Sentence is the meter's words: how far the cart is from free delivery, or
// that it has it, with the zones still charged extra. It is empty where the
// methods on offer disagree, and the meter is then not drawn.
func (f FreeDelivery) Sentence(ctx context.Context) string {
	switch f.Kind {
	case FreeDeliveryShort:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCartToFree), twd(f.ShortfallCents))
	case FreeDeliveryReached:
		if len(f.SurchargeZones) > 0 {
			return fmt.Sprintf(i18n.T(ctx, i18n.KeyFreeDeliveryReachedExcept),
				strings.Join(f.SurchargeZones, i18n.T(ctx, i18n.KeyListSeparator)))
		}
		return i18n.T(ctx, i18n.KeyFreeDeliveryReached)
	case FreeDeliveryUnstated:
		return ""
	default:
		panic("cart: unknown FreeDeliveryKind: " + string(f.Kind))
	}
}

// Note names the amount the meter measures against, where one is named.
func (f FreeDelivery) Note(ctx context.Context) string {
	switch {
	case f.ThresholdCents <= 0:
		return ""
	case f.Kind == FreeDeliveryShort:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingFreeOver), twd(f.ThresholdCents))
	case f.Kind == FreeDeliveryReached:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCartFactOver), twd(f.ThresholdCents))
	default:
		return ""
	}
}

// Meter is the bar's reading in cents: how much of the threshold the subtotal
// covers, capped at the threshold. ok is false where there is no one amount to
// measure against, and no bar is drawn.
func (f FreeDelivery) Meter(subtotalCents int64) (value, limit int64, ok bool) {
	if f.Kind == FreeDeliveryUnstated || f.ThresholdCents <= 0 {
		return 0, 0, false
	}
	if f.Kind == FreeDeliveryReached {
		return f.ThresholdCents, f.ThresholdCents, true
	}
	return min(max(subtotalCents, 0), f.ThresholdCents), f.ThresholdCents, true
}

// Percent is the bar's reading in whole percent, rounded down so a cart one
// dollar short never reads 100%.
func (f FreeDelivery) Percent(subtotalCents int64) string {
	value, limit, ok := f.Meter(subtotalCents)
	if !ok {
		return ""
	}
	return strconv.FormatInt(value*100/limit, 10) + "%"
}

// LineCount heads the lines: what the cart holds that can still be bought.
func (v View) LineCount(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyCartLineCount, v.ItemCount, v.ItemCount)
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

// drawerHold is why the bag drawer offers no checkout: the sentence the cart
// page says for the same cause, so the two cannot disagree.
func (v View) drawerHold(ctx context.Context) string {
	switch {
	case v.StockNotice(ctx) != "":
		return v.StockNotice(ctx)
	case v.MixedTaxTypes:
		return i18n.T(ctx, i18n.KeyCartMixedTaxTypes)
	case v.NoDelivery:
		return i18n.T(ctx, i18n.KeyShippingUnavailable)
	default:
		return ""
	}
}

func (v View) HasSoldOut() bool {
	return slices.ContainsFunc(v.Lines, func(l Line) bool { return l.Unavailable })
}

func (v View) HasNotice() bool { return v.Notice.Text != "" }

// ordersLink is the way on for somebody who came back to /checkout after placing an order:
// the account's orders for a signed-in reader, the order lookup for a guest.
func ordersLink(ctx context.Context) (href string, label i18n.Key) {
	if _, ok := user.FromContext(ctx); ok {
		return "/account#orders-heading", i18n.KeyContactMyOrders
	}
	return "/orders/find", i18n.KeyFindOrderTitle
}

// onSaleLead is 特價 as the head of the phrase that says until when a campaign
// price holds. Chinese runs it into 至 with no space; English parts the words.
func onSaleLead(ctx context.Context) string {
	if i18n.FromContext(ctx) == i18n.En {
		return i18n.T(ctx, i18n.KeyOnSale) + " "
	}
	return i18n.T(ctx, i18n.KeyOnSale)
}

// PayStartNote is the line under the checkout button that says when the stock is held and how long the customer has to start paying.
func (v View) PayStartNote(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyCartPayStart, v.PayStartMinutes, strconv.FormatInt(v.PayStartMinutes, 10))
}

func (v View) FromReorder() bool { return v.ReorderAdded > 0 || v.ReorderSkipped > 0 }

// ReorderOutcome is Refused when a reorder put nothing back, and Done otherwise: a partial
// reorder did put something back, and its sentence says what was left out.
func (v View) ReorderOutcome() widget.Outcome {
	if v.ReorderAdded == 0 && v.ReorderSkipped > 0 && !v.ReorderAdjusted {
		return widget.OutcomeRefused
	}
	return widget.OutcomeDone
}

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

// ReasonText is the word beside a line a reorder left out.
func (l SkippedLine) ReasonText(ctx context.Context) string {
	if l.Reason == SkipSoldOut {
		return i18n.T(ctx, i18n.KeySoldOut)
	}
	return i18n.T(ctx, i18n.KeyReorderGone)
}

func Meta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyCart)}
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
// the summary, the count over the lines, the notices, the free-delivery meter and
// the header's cart link. The stepper in use and the other lines are never replaced.
func cartLineSwap(variantID string) string {
	return "#line-body-" + variantID + ",#line-money-" + variantID +
		",#cart-summary,#cart-lines-heading,#cart-notices,#cart-link,#cart-count:innerHTML"
}
