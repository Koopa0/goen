package pages

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/fieldrule"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type CartLine struct {
	VariantID    string
	Slug         string
	Name         string
	Brand        string
	SKU          string
	Label        string
	TaxExempt    bool
	UnitCents    int64
	CompareCents int64
	Quantity     int32
	Available    int32

	Unavailable bool
	Short       bool

	ImageURL    string
	ImageSrcset string
	ImageAlt    string
}

func (l CartLine) UnitPrice() string { return twd(l.UnitCents) }

func (l CartLine) LineTotal() string { return twd(l.UnitCents * int64(l.effectiveQuantity())) }

func (l CartLine) effectiveQuantity() int32 {
	if l.Unavailable {
		return 0
	}
	if l.Short {
		return l.Available
	}
	return l.Quantity
}

func (l CartLine) PricedQuantityText() string {
	return strconv.FormatInt(int64(l.effectiveQuantity()), 10)
}

func (l CartLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

func (l CartLine) AvailableText() string { return strconv.FormatInt(int64(l.Available), 10) }

func (l CartLine) MaxQuantity() string {
	n := max(min(l.Available, 999), 1)
	return strconv.FormatInt(int64(n), 10)
}

func (l CartLine) HasImage() bool { return l.ImageURL != "" }

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
	Destination    destination.Kind
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

	PickupChain     pickup.Chain
	PickupStoreCode string
	PickupStoreName string

	Note string
}

func (v *CheckoutView) ToPickupPoint() bool { return v.Destination == destination.PickupPoint }

type PickupChainChoice struct {
	Value pickup.Chain
	Label string
}

// PickupChainChoices is every chain the shop can accept, so an order placed at one
// of them stays correctable in the back office.
func PickupChainChoices() []PickupChainChoice {
	return choicesFor(pickup.Offered())
}

// CheckoutPickupChainChoices is the part shoppers may choose today: the two chains
// whose store picker the shop integrates first.
func CheckoutPickupChainChoices() []PickupChainChoice {
	return choicesFor([]pickup.Chain{pickup.SevenEleven, pickup.FamilyMart})
}

func choicesFor(chains []pickup.Chain) []PickupChainChoice {
	out := make([]PickupChainChoice, 0, len(chains))
	for _, c := range chains {
		out = append(out, PickupChainChoice{Value: c, Label: pickupChainLabel(c)})
	}
	return out
}

func pickupChainLabel(code pickup.Chain) string {
	switch code {
	case "":
		return ""
	case pickup.SevenEleven:
		return "7-ELEVEN"
	case pickup.FamilyMart:
		return "全家 FamilyMart" // i18n-exempt: a chain's own name, already bilingual
	case pickup.HiLife:
		return "萊爾富 Hi-Life" // i18n-exempt: a chain's own name, already bilingual
	case pickup.OKMart:
		return "OK mart"
	default:
		panic("pages: unknown pickup chain: " + string(code))
	}
}

type Delivery struct {
	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     pickup.Chain
	PickupStoreCode string
	PickupStoreName string
}

// IsPickup reads the chain as the destination: the store behind it comes from the
// carrier's picker and is absent on an order placed before one existed.
func (d Delivery) IsPickup() bool { return d.PickupChain != "" }

func (d Delivery) Line() string {
	if d.IsPickup() {
		line := pickupChainLabel(d.PickupChain)
		if d.PickupStoreName != "" {
			line += " " + d.PickupStoreName
		}
		if d.PickupStoreCode != "" {
			line += "(" + d.PickupStoreCode + ")"
		}
		return line
	}
	return strings.TrimSpace(d.PostalCode + " " + d.City + d.District + d.Street)
}

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
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCheckoutSubmitNote), PayStartMinutesText())
	}
	if v.UsesCredit() {
		return i18n.T(ctx, i18n.KeyCheckoutCreditSubmitNote)
	}
	return i18n.T(ctx, i18n.KeyCheckoutZeroSubmitNote)
}

type OrderLine struct {
	SKU         string
	Name        string
	Label       string
	UnitCents   int64
	Quantity    int32
	ImageURL    string
	ImageSrcset string
	ImageAlt    string
	// WarrantyMonths is the promise the line copied at checkout; zero when it carried none.
	WarrantyMonths int
	// Registered counts the units this line's share has registered, and WarrantyUntil is the day their cover ends.
	Registered    int
	WarrantyUntil time.Time
}

func (l OrderLine) HasImage() bool { return l.ImageURL != "" }

func (l OrderLine) UnitPrice() string { return twd(l.UnitCents) }

func (l OrderLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

func (l OrderLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// OrderEvent carries no actor: anyone holding the order number can reach this page.
type OrderEvent struct {
	Kind order.EventKind
	Note string
	At   time.Time
}

// ShowsNote reports whether the note belongs on the customer's history. A shipped event's note is the
// carrier's name and tracking number written in the shop's language, which the parcel already states in the
// reader's own.
func (e OrderEvent) ShowsNote() bool { return e.Note != "" && e.Kind != order.EventShipped }

// LabelKey is the short word the order's own history uses for the event.
func (e OrderEvent) LabelKey() i18n.Key {
	switch e.Kind {
	case order.EventPlaced:
		return i18n.KeyEventPlaced
	case order.EventPaid:
		return i18n.KeyStatusPaid
	case order.EventPicking:
		return i18n.KeyEventPicking
	case order.EventShipped:
		return i18n.KeyEventShipped
	case order.EventInTransit:
		return i18n.KeyStatusInTransit
	case order.EventDelivered:
		return i18n.KeyEventDelivered
	case order.EventCompleted:
		return i18n.KeyStatusCompleted
	case order.EventCancelled:
		return i18n.KeyEventCancelled
	case order.EventRefunded:
		return i18n.KeyStatusRefunded
	default:
		panic("pages: no label for order event kind " + string(e.Kind))
	}
}

// LookupLabelKey is LabelKey for a reader that must survive a kind it does not
// know.
func (e OrderEvent) LookupLabelKey() (i18n.Key, bool) {
	switch e.Kind {
	case order.EventPlaced:
		return i18n.KeyStatusPlaced, true
	case order.EventPaid:
		return i18n.KeyStatusPaid, true
	case order.EventPicking:
		return i18n.KeyStatusPicking, true
	case order.EventShipped:
		return i18n.KeyStatusShipped, true
	case order.EventInTransit:
		return i18n.KeyStatusInTransit, true
	case order.EventDelivered:
		return i18n.KeyStatusDelivered, true
	case order.EventCompleted:
		return i18n.KeyStatusCompleted, true
	case order.EventCancelled:
		return i18n.KeyStatusCancelled, true
	case order.EventRefunded:
		return i18n.KeyStatusRefunded, true
	default:
		return "", false
	}
}

// OrderShipment is one parcel. The right to cancel and the warranty of the lines in it count from its own delivery.
type OrderShipment struct {
	Carrier   carrier.Carrier
	Tracking  string
	ShippedAt time.Time
	// DeliveredAt is zero until the parcel arrives, or is collected from a store.
	DeliveredAt time.Time
	// RescissionEnds and GoodwillEnds are the last day of the right to cancel and the last day of unused
	// returns, as the database reads them; zero until the parcel is delivered.
	RescissionEnds time.Time
	GoodwillEnds   time.Time
	// Lines are the units of each line that went in this parcel.
	Lines []OrderLine
}

func (s OrderShipment) TrackURL() string { return s.Carrier.TrackingURL(s.Tracking) }

func (s OrderShipment) Delivered() bool { return !s.DeliveredAt.IsZero() }

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

type OrderView struct {
	Number       string
	Status       order.FulfillmentStatus
	Email        string
	ShippingName string
	DeliveryTo   string
	PlacedAt     time.Time
	// Now is the moment the page is read; the days left and the grids count from it.
	Now time.Time
	// HoldUntil is the earliest stored reservation expiry, zero without rows; Checkout Session expiry is bound to it.
	HoldUntil time.Time
	// Pickup is set for an order collected from a store, where delivery reads as collection.
	Pickup bool
	// Lines are the lines as bought; Unshipped are the units no parcel carries yet.
	Lines          []OrderLine
	Unshipped      []OrderLine
	Returned       *OrderReturned
	SubtotalCents  int64
	ShippingCents  int64
	DiscountCents  int64
	DiscountReason string
	CreditCents    int64
	TaxCents       int64
	Timeline       []OrderEvent
	Shipments      []OrderShipment
	Invoice        *OrderInvoice
	Cancelled      bool
	Committed      bool
	OwedCents      int64
	// ShowWarrantyLink is set only when a signed-in account owns the order: guest-token
	// viewers must not see account-only registration.
	ShowWarrantyLink bool
	// PaymentRefreshURL is a bounded presentation hint, never evidence of payment.
	PaymentRefreshURL string
	// PaymentConfirmationPending preserves the return hint after checks stop; it never changes payment facts.
	PaymentConfirmationPending bool
	// PaymentReturnHint is untrusted; it only hides cancellation until the stored stock hold ends.
	PaymentReturnHint bool
	// The bounds behind that URL, quoted in the notice so the copy cannot drift from the handler.
	PaymentRefreshSeconds, PaymentRefreshChecks int
	// Where payments are off, a link to the payment page would lead to a page that sends the shopper back here.
	PaymentsEnabled bool
}

// OrderReturned is an order whose every unit is in a return whose refund has settled. At is the day the last of them was refunded.
type OrderReturned struct {
	At          time.Time
	RefundCents int64
}

type PaymentState string

const (
	PaymentNone     PaymentState = ""
	PaymentAwaiting PaymentState = "awaiting"
	PaymentPaid     PaymentState = "paid"
	PaymentRefunded PaymentState = "refunded"
)

// PaymentState reads the order's own facts: a cancelled order is refunded only where
// a refund is on its timeline, and a partial refund on a delivered order leaves it paid.
func (v *OrderView) PaymentState() PaymentState {
	switch {
	case v.Returned != nil:
		return PaymentRefunded
	case v.Status == order.FulfillmentCancelled:
		for _, e := range v.Timeline {
			if e.Kind == order.EventRefunded {
				return PaymentRefunded
			}
		}
		return PaymentNone
	case v.AwaitingPayment():
		return PaymentAwaiting
	default:
		return PaymentPaid
	}
}

func (s PaymentState) Key() i18n.Key {
	switch s {
	case PaymentAwaiting:
		return i18n.KeyStatusAwaitingPayment
	case PaymentRefunded:
		return i18n.KeyStatusRefunded
	default:
		return i18n.KeyStatusPaid
	}
}

// StateKey leaves paying to its own row, so a placed order stays 訂單已送出 until it moves.
func (v *OrderView) StateKey() i18n.Key {
	if v.Returned != nil {
		return i18n.KeyStatusRefunded
	}
	switch v.Status {
	case order.FulfillmentPicking:
		return i18n.KeyStatusPicking
	case order.FulfillmentShipped:
		return i18n.KeyStatusShipped
	case order.FulfillmentDelivered:
		return i18n.KeyStatusDelivered
	case order.FulfillmentCompleted:
		return i18n.KeyStatusCompleted
	case order.FulfillmentCancelled:
		return i18n.KeyStatusCancelled
	default:
		return i18n.KeyOrderPlaced
	}
}

func (v *OrderView) Owed() string { return twd(v.OwedCents) }

func (v *OrderView) CanCancel() bool {
	return v.Status == order.FulfillmentPending && !v.Committed
}

// ShowCancel keeps an unresolved return from racing cancellation before the stock hold ends.
func (v *OrderView) ShowCancel() bool {
	return v.CanCancel() && v.PaymentRefreshURL == "" && !v.paymentReturnHoldsCancel()
}

func (v *OrderView) paymentReturnHoldsCancel() bool {
	return v.PaymentReturnHint && v.AwaitingPayment() && v.HoldUntil.After(v.Now)
}

// CancelVoidsInvoice reports whether cancelling voids the order's 統一發票:
// store credit paid it in full, and the invoice was owed then.
func (v *OrderView) CancelVoidsInvoice() bool { return v.CreditCents > 0 && v.OwedCents == 0 }

func OrderMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderMeta), number)}
}

func (v *OrderView) Subtotal() string { return twd(v.SubtotalCents) }

func (v *OrderView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

func (v *OrderView) Discount() string { return "-" + twd(v.DiscountCents) }

func DiscountLabel(ctx context.Context, reason string) string {
	if reason == "" {
		return i18n.T(ctx, i18n.KeyDiscount)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountFor), reason)
}

func (v *OrderView) Total() string { return twd(v.TotalCents()) }

func (v *OrderView) TotalCents() int64 {
	return v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents
}

// UsedCredit gates the credit row: without it the summary and the payment page
// state different figures, since the credit is debited in the order's own transaction.
func (v *OrderView) UsedCredit() bool { return v.CreditCents > 0 }

func (v *OrderView) Credit() string { return "-" + twd(v.CreditCents) }

func (v *OrderView) CanRequestReturn() bool {
	if v.Returned != nil {
		return false
	}
	switch v.Status {
	case order.FulfillmentShipped, order.FulfillmentDelivered, order.FulfillmentCompleted:
		return true
	default:
		return false
	}
}

func (v *OrderView) WarrantyLink() string { return "/account/warranty/" + v.Number }

func (v *OrderView) AwaitingPayment() bool {
	return awaitingPayment(v.Status, v.Committed, v.OwedCents)
}

func (v *CheckoutView) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

func (v *CheckoutView) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }

func (i CheckoutInvoice) NeedsDonationCode() bool { return i.Chosen() == invoice.PreferenceDonate }

type OrderInvoice struct {
	Documents     []OrderInvoiceDocument
	Type          invoice.Preference
	MobileBarcode string
	DonationCode  string
	TaxID         string
}

type OrderInvoiceDocument struct {
	Allowance   bool
	Number      string
	RandomCode  string
	AmountCents int64
	Voided      bool
	IssuedOn    string
}

func (d OrderInvoiceDocument) Label(ctx context.Context) string {
	if d.Allowance {
		return i18n.T(ctx, i18n.KeyAdminDocAllowance)
	}
	return i18n.T(ctx, i18n.KeyAdminDocInvoice)
}

func (d OrderInvoiceDocument) Amount() string { return twd(d.AmountCents) }

// ChoiceText masks a mobile carrier: it is a key to somebody's invoice archive and
// the page may be read from a link.
func (i *OrderInvoice) ChoiceText(ctx context.Context) string {
	switch i.Type {
	case invoice.PreferenceMember:
		return i18n.T(ctx, i18n.KeyAdminInvoiceCarrierMember)
	case invoice.PreferenceMobile:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceCarrierMobileBarcode), maskMobileBarcode(i.MobileBarcode))
	case invoice.PreferenceDonate:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceDonate), i.DonationCode)
	case invoice.PreferenceCompany:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceTaxID), i.TaxID)
	default:
		panic("pages: no label for invoice type " + string(i.Type))
	}
}

func maskMobileBarcode(code string) string {
	if len(code) <= 3 {
		return code
	}
	return code[:1] + strings.Repeat("*", len(code)-3) + code[len(code)-2:]
}

// cartLineSwap names what one quantity update changes: that line's text and price,
// the summary, the item count, the notices and the header's cart link. The stepper
// in use and the other lines are never replaced.
func cartLineSwap(variantID string) string {
	return "#line-body-" + variantID + ",#line-money-" + variantID +
		",#cart-summary,#cart-notices,#cart-link,#cart-count:innerHTML"
}
