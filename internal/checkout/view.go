package checkout

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/fieldrule"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

type View struct {
	Cart                cart.View
	Shipping            []cart.ShippingChoice
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
	Address        Address
	Errors         map[string]string
	Invoice        InvoiceView
	InvoiceChoices []InvoiceChoice
	PickupChains   []pages.PickupChainChoice
	SavedAddresses []SavedAddress
	ChosenAddress  string
	Profile        Profile
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
	// QuoteID is opaque to the page: this package creates it and the page carries it back unchanged.
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

func (v *View) HasShipping() bool { return len(v.Shipping) > 0 }

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

func (v *View) OffersTheStoreMap() bool {
	return v.ToPickupPoint() && v.MapOffered
}

// HasPickupStore needs both halves, because order_private_data refuses a row carrying one without the other.
func (v *View) HasPickupStore() bool {
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
// The patterns accept what delivery.Details.Trim accepts (the server trims first) and repeat
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

type Address struct {
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

func (v *View) ToPickupPoint() bool { return v.Destination == shipping.DestinationPickupPoint }

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

type Profile struct {
	Email string
	Name  string
	Phone string
}

func (v *View) OffersTheProfile() bool {
	return v.Profile.Email != "" && (v.Profile.Name != "" || v.Profile.Phone != "")
}

func (v *View) OffersTheAddressBook() bool {
	return !v.ToPickupPoint() && len(v.SavedAddresses) > 0
}

func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCheckoutTitle)}
}

func (v *View) Err(field string) string { return v.Errors[field] }

func (v *View) HasErr(field string) bool { return v.Errors[field] != "" }

func (v *View) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

func (v *View) AnyErrors() bool { return len(v.Errors) > 0 }

// OnlyCouponRefused lets the banner name the code instead of sending the shopper
// through the fields to find it.
func (v *View) OnlyCouponRefused() bool {
	return len(v.Errors) == 1 && v.HasErr("coupon")
}

func (v *View) HasSurcharge() bool { return v.SurchargeCents > 0 }

func (v *View) Surcharge() string { return twd(v.SurchargeCents) }

func (v *View) ShippingFeeCents() int64 {
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

func (v *View) BaseShippingCents() int64 {
	return v.ShippingFeeCents() - v.SurchargeCents
}

func (v *View) ShippingText() string { return twd(v.BaseShippingCents()) }

func (v *View) ShipsFree() bool {
	return v.CouponFreeShipping || v.BaseShippingCents() == 0
}

// ChargedShippingCents is what PlaceOrder stores: a free-shipping coupon removes
// the base rate but deliberately not a remote-zone surcharge.
func (v *View) ChargedShippingCents() int64 {
	if v.CouponFreeShipping {
		return v.SurchargeCents
	}
	return v.ShippingFeeCents()
}

func (v *View) Total() string {
	return twd(v.TotalCents())
}

func (v *View) GrossCents() int64 {
	return v.Cart.SubtotalCents + v.ChargedShippingCents() - v.CouponDiscountCents
}

// CreditCents is the credit the displayed quote applies: a rise in balance after
// rendering never spends more; a fall that cannot fund it makes placement refresh the quote.
func (v *View) CreditCents() int64 {
	return min(v.AvailableCreditCents, v.GrossCents())
}

func (v *View) UsesCredit() bool { return v.CreditCents() > 0 }

func (v *View) Credit() string { return "-" + twd(v.CreditCents()) }

func (v *View) TotalCents() int64 { return v.GrossCents() - v.CreditCents() }

func (v *View) PlaceOrderKey() i18n.Key {
	if v.TotalCents() > 0 {
		return i18n.KeyPlaceOrderAndPay
	}
	return i18n.KeyPlaceOrder
}

func (v *View) PlacementNote(ctx context.Context) string {
	if v.TotalCents() > 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCheckoutSubmitNote), strconv.FormatInt(v.Cart.PayStartMinutes, 10))
	}
	if v.UsesCredit() {
		return i18n.T(ctx, i18n.KeyCheckoutCreditSubmitNote)
	}
	return i18n.T(ctx, i18n.KeyCheckoutZeroSubmitNote)
}

type InvoiceView struct {
	Type          invoice.Preference
	MobileBarcode string
	DonationCode  string
	CompanyName   string
	TaxID         string
}

func (i InvoiceView) Is(t invoice.Preference) bool { return i.Chosen() == t }

func (i InvoiceView) Chosen() invoice.Preference {
	if i.Type == "" {
		return invoice.PreferenceMember
	}
	return i.Type
}

// NeedsMobileBarcode and NeedsTaxID decide which half of the form exists:
// rendering both would leave required promising something the server will not demand.
func (i InvoiceView) NeedsMobileBarcode() bool { return i.Chosen().NeedsMobileBarcode() }

func (i InvoiceView) NeedsTaxID() bool { return i.Chosen().NeedsTaxID() }

func (v *View) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

func (v *View) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }

func (i InvoiceView) NeedsDonationCode() bool { return i.Chosen() == invoice.PreferenceDonate }
