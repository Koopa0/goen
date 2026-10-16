package pages

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/shipping"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/fieldrule"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type CartView struct {
	Lines         []CartLine
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
func (f FreeDelivery) Stat(ctx context.Context) components.Stat {
	switch f.Kind {
	case FreeDeliveryShort:
		return components.Stat{
			Label: i18n.T(ctx, i18n.KeyCartFactToFree),
			Value: components.StatMoney(f.ShortfallCents),
			Note:  fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingFreeOver), twd(f.ThresholdCents)),
		}
	case FreeDeliveryReached:
		s := components.Stat{Label: i18n.T(ctx, i18n.KeyShippingFee), Value: components.StatWord(f.Word(ctx))}
		if f.ThresholdCents > 0 {
			s.Note = fmt.Sprintf(i18n.T(ctx, i18n.KeyCartFactOver), twd(f.ThresholdCents))
		}
		return s
	default:
		return components.Stat{}
	}
}

// Facts are the cart's totals as they stand: a sold-out line is in neither the count nor the subtotal.
func (v CartView) Facts(ctx context.Context) []components.Stat {
	return []components.Stat{
		{Label: i18n.T(ctx, i18n.KeyCartFactItems), Value: components.StatCount(v.ItemCount, i18n.T(ctx, i18n.KeyFactUnitItems))},
		{Label: i18n.T(ctx, i18n.KeySubtotal), Value: components.StatMoney(v.SubtotalCents)},
		v.FreeDelivery.Stat(ctx),
	}
}

// StockNotice says what stands between the shopper and checkout; a sold-out line outranks a short one.
func (v CartView) StockNotice(ctx context.Context) string {
	switch {
	case v.HasSoldOut():
		return i18n.T(ctx, i18n.KeyCartSoldOut)
	case v.Blocked():
		return i18n.T(ctx, i18n.KeyCartStockShort)
	default:
		return ""
	}
}

func (v CartView) HasSoldOut() bool {
	return slices.ContainsFunc(v.Lines, func(l CartLine) bool { return l.Unavailable })
}

func (v CartView) HasNotice() bool { return v.Notice != "" }

// HoldNote is the line under the checkout button that says how long placing the order holds the stock.
func (v CartView) HoldNote(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyCartHold, v.HoldMinutes, strconv.FormatInt(v.HoldMinutes, 10))
}

func (v CartView) FromReorder() bool { return v.ReorderAdded > 0 || v.ReorderSkipped > 0 }

func (v CartView) ReorderText(ctx context.Context) string {
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

func CartMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCart)}
}

func (v CartView) Empty() bool { return len(v.Lines) == 0 }

func (v CartView) Subtotal() string { return twd(v.SubtotalCents) }

func (v CartView) Blocked() bool {
	for i := range v.Lines {
		if v.Lines[i].Unavailable || v.Lines[i].Short {
			return true
		}
	}
	return false
}

func (v CartView) CanCheckout() bool { return !v.Empty() && !v.Blocked() && !v.MixedTaxTypes }

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

type CheckoutView struct {
	Cart                CartView
	Shipping            []ShippingChoice
	Chosen              string
	QuotedShippingCents int64
	SurchargeCents      int64
	// Repriced is a quote the customer has not seen yet, not an Errors entry: nothing
	// they typed was wrong. Still a 422, so nobody is charged for an unconfirmed quote.
	Repriced string
	// CreditChanged is a notice like Repriced: the customer must see the new figure before submitting again.
	CreditChanged string
	ZoneName      string
	// Destination is decided by the server; no field carries it back.
	Destination    shipping.Destination
	Address        CheckoutAddress
	Errors         map[string]string
	Invoice        CheckoutInvoice
	InvoiceChoices []InvoiceChoice
	PickupChains   []PickupChainChoice
	SavedAddresses []SavedAddress
	ChosenAddress  string
	Profile        CheckoutProfile
	RecipientMe    bool
	// What the recipient fields held just before the box was ticked, which
	// unticking puts back.
	RecipientPrevName    string
	RecipientPrevPhone   string
	CouponCode           string
	CouponApplied        string
	CouponDiscountCents  int64
	CouponFreeShipping   bool
	AvailableCreditCents int64
	// QuoteID is opaque to the page: the cart package creates it and the page carries it back unchanged.
	QuoteID        string
	IdempotencyKey string
	// MapOffered needs a configured carrier that serves the chosen chain. The picker's
	// own form is built only where it is opened, by PickupStart.
	MapOffered bool
	// PickupNonce decides whether the store was honoured: it travels to the carrier in
	// ExtraData and back in the URL, and is checked against a cookie only this browser holds.
	PickupNonce string
	// PickupStoreAddr is DISPLAY ONLY: read from the URL for the summary, never a
	// hidden field, never posted, never stored (the schema keeps a code and a name).
	PickupStoreAddr string
	// PickupRefused: a store arrived that this browser cannot vouch for; the page echoes none of it.
	PickupRefused bool
}

func (v *CheckoutView) HasShipping() bool { return len(v.Shipping) > 0 }

// PickupStartAction is goen's own route, so what the shopper typed reaches only
// goen; the carrier's map form is built on the page it answers.
const PickupStartAction = "/checkout/pickup/start"

// PickupMapPath is a GET holding the carrier's form, so Back from the map lands on
// a page and not on a POST.
const PickupMapPath = "/checkout/pickup/map"

// formnovalidate: a shopper who has not typed an email yet must still be able to
// choose a store; nothing is placed by this submit. The button carries the store
// refusal because the store is chosen through the picker, not typed.
func checkoutStoreButtonAttrs(invalid string, refused bool) templ.Attributes {
	attrs := templ.Attributes{
		"formaction": PickupStartAction, "formmethod": "post", "formnovalidate": true,
		"aria-invalid": invalid,
	}
	if refused {
		attrs["aria-describedby"] = "pickup_store-error"
	}
	return attrs
}

// CheckoutMapForm holds only parameters ECPay's map documents, so the
// template cannot add one carrying something the shopper typed.
type CheckoutMapForm struct {
	Action          string
	MerchantID      string
	MerchantTradeNo string
	LogisticsType   string
	// LogisticsSubType is the chain in the spelling the merchant's contract uses; B2C
	// and C2C spell the same chain differently.
	LogisticsSubType string
	IsCollection     string
	ServerReplyURL   string
	ExtraData        string
	// Device is sent only to a phone, and only 7-ELEVEN reads it.
	Device string
}

func (v *CheckoutView) OffersTheStoreMap() bool {
	return v.ToPickupPoint() && v.MapOffered
}

// HasPickupStore needs both halves, because order_private_data refuses a row carrying one without the other.
func (v *CheckoutView) HasPickupStore() bool {
	return v.Address.PickupStoreCode != "" && v.Address.PickupStoreName != ""
}

type checkoutFieldHint struct {
	Autocomplete      string
	InputMode         string
	AutoCapitalize    string
	SpellCheck        string
	Pattern           string
	ConstraintMessage i18n.Key
	Rule              *fieldrule.Rule
}

func (h checkoutFieldHint) Constrained() bool { return h.Pattern != "" || h.Rule != nil }

func (h checkoutFieldHint) ConstraintText(ctx context.Context) string {
	if h.Rule != nil {
		return i18n.T(ctx, h.Rule.Message)
	}
	return i18n.T(ctx, h.ConstraintMessage)
}

// Every helper-rendered control must make an explicit autofill decision.
//
// Do not add enterkeyhint: Enter in any checkout field places the order, so a
// "next" hint would label a key that charges the customer.
// The patterns accept what cart.Trim() accepts (the server trims first) and repeat
// its bounds (maxPostalCodeRunes, maxCityRunes).
var checkoutFieldHints = map[string]checkoutFieldHint{
	"email":       {Autocomplete: "email", Rule: &fieldrule.Email},
	"name":        {Autocomplete: "name"},
	"phone":       {Autocomplete: "tel", Rule: &fieldrule.Phone},
	"postal_code": {Autocomplete: "postal-code", Rule: &fieldrule.PostalCode},
	"city":        {Autocomplete: "address-level1", Pattern: `\s*\S(?:.{0,18}\S)?\s*`, ConstraintMessage: i18n.KeyCheckoutRegionLength},
	"district":    {Autocomplete: "address-level2", Pattern: `\s*\S(?:.{0,18}\S)?\s*`, ConstraintMessage: i18n.KeyCheckoutRegionLength},
}

func checkoutHintsFor(name string) checkoutFieldHint { return checkoutFieldHints[name] }

// The request is the same POST as the 更新 button's, so the write face is unchanged
// and the button remains the path with scripting off.
// show:none keeps the swap where the customer is looking: they pressed a radio
// half way down the form.
func checkoutChoiceSwap(which string) templ.Attributes {
	return templ.Attributes{
		"hx-include": "#checkout-form",
		"hx-post":    "/checkout",
		"hx-select":  "#checkout-region",
		"hx-swap":    "outerHTML show:none",
		"hx-target":  "#checkout-region",
		"hx-trigger": "change",
		"hx-vals":    `{"update":"` + which + `"}`,
	}
}

// With scripting, 套用 swaps only the code's field, the totals and the quote; without
// it the same submit comes back at the coupon field.
// The hidden quote is swapped too: a stale one would make the next 送出訂單 answer
// "the checkout changed".
func couponButtonAttrs(ctx context.Context) templ.Attributes {
	return templ.Attributes{
		"name":              "update",
		"value":             "coupon",
		"formaction":        "/checkout#coupon-field",
		"hx-post":           "/checkout",
		"hx-include":        "#checkout-form",
		"hx-vals":           `{"update":"coupon"}`,
		"hx-swap":           "none",
		"hx-select-oob":     "#coupon,#coupon-message:innerHTML,#summary-totals,#checkout-quote",
		"data-coupon-apply": true,
		"data-busy":         i18n.T(ctx, i18n.KeyTooManyRequests),
		"data-failed":       i18n.T(ctx, i18n.KeyCouponUnavailable),
	}
}

type InvoiceChoice struct {
	Value invoice.Preference
	Label string
}

type CheckoutAddress struct {
	Email      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     shipping.Chain
	PickupStoreCode string
	PickupStoreName string

	Note string
}

func (v *CheckoutView) ToPickupPoint() bool { return v.Destination == shipping.DestinationPickupPoint }

type SavedAddress struct {
	ID         string
	Label      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string
	Default    bool
}

func (a SavedAddress) Line() string {
	return strings.TrimSpace(a.PostalCode + " " + a.City + a.District + a.Street)
}

func (a SavedAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

const OtherAddress = "new"

type CheckoutProfile struct {
	Email string
	Name  string
	Phone string
}

func (v *CheckoutView) OffersTheProfile() bool {
	return v.Profile.Email != "" && (v.Profile.Name != "" || v.Profile.Phone != "")
}

func (v *CheckoutView) OffersTheAddressBook() bool {
	return !v.ToPickupPoint() && len(v.SavedAddresses) > 0
}

func CheckoutMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCheckoutTitle)}
}

func (v *CheckoutView) Err(field string) string { return v.Errors[field] }

func (v *CheckoutView) HasErr(field string) bool { return v.Errors[field] != "" }

func (v *CheckoutView) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

func (v *CheckoutView) AnyErrors() bool { return len(v.Errors) > 0 }

// OnlyCouponRefused lets the banner name the code instead of sending the shopper
// through the fields to find it.
func (v *CheckoutView) OnlyCouponRefused() bool {
	return len(v.Errors) == 1 && v.HasErr("coupon")
}

func (v *CheckoutView) HasSurcharge() bool { return v.SurchargeCents > 0 }

func (v *CheckoutView) Surcharge() string { return twd(v.SurchargeCents) }

func (v *CheckoutView) ShippingFeeCents() int64 {
	if v.QuotedShippingCents > 0 {
		return v.QuotedShippingCents
	}
	for i := range v.Shipping {
		if v.Shipping[i].VersionID == v.Chosen {
			return v.Shipping[i].FeeCents
		}
	}
	if len(v.Shipping) > 0 {
		return v.Shipping[0].FeeCents
	}
	return 0
}

func (v *CheckoutView) BaseShippingCents() int64 {
	return v.ShippingFeeCents() - v.SurchargeCents
}

func (v *CheckoutView) ShippingText() string { return twd(v.BaseShippingCents()) }

func (v *CheckoutView) ShipsFree() bool {
	return v.CouponFreeShipping || v.BaseShippingCents() == 0
}

// ChargedShippingCents is what PlaceOrder stores: a free-shipping coupon removes
// the base rate but deliberately not a remote-zone surcharge.
func (v *CheckoutView) ChargedShippingCents() int64 {
	if v.CouponFreeShipping {
		return v.SurchargeCents
	}
	return v.ShippingFeeCents()
}

func (v *CheckoutView) Total() string {
	return twd(v.TotalCents())
}

func (v *CheckoutView) GrossCents() int64 {
	return v.Cart.SubtotalCents + v.ChargedShippingCents() - v.CouponDiscountCents
}

// CreditCents is the credit the displayed quote applies: a rise in balance after
// rendering never spends more; a fall that cannot fund it makes placement refresh the quote.
func (v *CheckoutView) CreditCents() int64 {
	return min(v.AvailableCreditCents, v.GrossCents())
}

func (v *CheckoutView) UsesCredit() bool { return v.CreditCents() > 0 }

func (v *CheckoutView) Credit() string { return "-" + twd(v.CreditCents()) }

func (v *CheckoutView) TotalCents() int64 { return v.GrossCents() - v.CreditCents() }

func (v *CheckoutView) PlaceOrderKey() i18n.Key {
	if v.TotalCents() > 0 {
		return i18n.KeyPlaceOrderAndPay
	}
	return i18n.KeyPlaceOrder
}

func (v *CheckoutView) PlacementNote(ctx context.Context) string {
	if v.TotalCents() > 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCheckoutSubmitNote), strconv.FormatInt(v.Cart.PayStartMinutes, 10))
	}
	if v.UsesCredit() {
		return i18n.T(ctx, i18n.KeyCheckoutCreditSubmitNote)
	}
	return i18n.T(ctx, i18n.KeyCheckoutZeroSubmitNote)
}

type CheckoutInvoice struct {
	Type          invoice.Preference
	MobileBarcode string
	DonationCode  string
	CompanyName   string
	TaxID         string
}

func (i CheckoutInvoice) Is(t invoice.Preference) bool { return i.Chosen() == t }

func (i CheckoutInvoice) Chosen() invoice.Preference {
	if i.Type == "" {
		return invoice.PreferenceMember
	}
	return i.Type
}

// NeedsMobileBarcode and NeedsTaxID decide which half of the form exists:
// rendering both would leave required promising something the server will not demand.
func (i CheckoutInvoice) NeedsMobileBarcode() bool { return i.Chosen().NeedsMobileBarcode() }

func (i CheckoutInvoice) NeedsTaxID() bool { return i.Chosen().NeedsTaxID() }

func (v *CheckoutView) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

func (v *CheckoutView) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }

func (i CheckoutInvoice) NeedsDonationCode() bool { return i.Chosen() == invoice.PreferenceDonate }

// cartLineSwap names what one quantity update changes: that line's text and price,
// the summary, the item count, the notices and the header's cart link. The stepper
// in use and the other lines are never replaced.
func cartLineSwap(variantID string) string {
	return "#line-body-" + variantID + ",#line-money-" + variantID +
		",#cart-summary,#cart-notices,#cart-link,#cart-count:innerHTML"
}
