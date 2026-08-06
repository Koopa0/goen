package pages

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// CartLine is one line in the cart.
type CartLine struct {
	VariantID    string
	Slug         string
	Name         string
	Brand        string
	SKU          string
	Label        string // "星霧藍 · 512GB", or "" for a product with no options
	UnitCents    int64
	CompareCents int64
	Quantity     int32
	Available    int32

	// Unavailable is a line the catalogue can no longer honour at all;
	// Short is one where fewer remain than the cart asks for. Both block
	// checkout, and both are said per line rather than as one vague banner.
	Unavailable bool
	Short       bool

	ImageURL string
	ImageAlt string
}

// UnitPrice is what one of this line costs.
func (l CartLine) UnitPrice() string { return twd(l.UnitCents) }

// LineTotal is the unit price times the quantity that can actually be supplied.
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

// QuantityText is how many the cart holds.
func (l CartLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// AvailableText is how many remain.
func (l CartLine) AvailableText() string { return strconv.FormatInt(int64(l.Available), 10) }

// MaxQuantity bounds the line's quantity input to what can be supplied.
func (l CartLine) MaxQuantity() string {
	n := l.Available
	if n > 999 {
		n = 999
	}
	if n < 1 {
		n = 1
	}
	return strconv.FormatInt(int64(n), 10)
}

// HasImage reports whether the thumbnail has an image.
func (l CartLine) HasImage() bool { return l.ImageURL != "" }

// CartView is the cart page.
type CartView struct {
	Lines         []CartLine
	SubtotalCents int64
	ItemCount     int64
	// What a 再買一次 put back, and what it could not. Counts rather than
	// names: they arrive through a redirect, and what somebody bought is not a
	// thing to write into a URL.
	ReorderAdded   int
	ReorderSkipped int
}

// FromReorder reports whether this page is showing the result of a 再買一次.
func (v CartView) FromReorder() bool { return v.ReorderAdded > 0 || v.ReorderSkipped > 0 }

// ReorderText is what the reorder came to, in a sentence.
//
// The skipped count is named rather than hidden: a reorder that quietly drops
// two of five lines is a customer who checks out with the wrong basket.
func (v CartView) ReorderText(ctx context.Context) string {
	switch {
	case v.ReorderSkipped == 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderAll), v.ReorderAdded)
	case v.ReorderAdded == 0:
		return i18n.T(ctx, i18n.KeyReorderNone)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderPartial), v.ReorderAdded, v.ReorderSkipped)
	}
}

// CartMeta is the chrome view model for the cart.
//
// A function of the request rather than a package var, because a page title is
// chrome and chrome follows the visitor's language. Every Meta in this package
// went the same way for the same reason.
func CartMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCart)}
}

// Empty reports whether the cart holds nothing.
func (v CartView) Empty() bool { return len(v.Lines) == 0 }

// Subtotal is the money the lines add up to.
func (v CartView) Subtotal() string { return twd(v.SubtotalCents) }

// ItemCountText is how many units are in the cart.
func (v CartView) ItemCountText() string { return strconv.FormatInt(v.ItemCount, 10) }

// Blocked reports whether any line stops checkout. A cart holding something
// that cannot be supplied must not reach the checkout form: the order would
// fail at the inventory hold, after the visitor typed an address.
func (v CartView) Blocked() bool {
	for i := range v.Lines {
		if v.Lines[i].Unavailable || v.Lines[i].Short {
			return true
		}
	}
	return false
}

// CanCheckout reports whether the checkout button is live.
func (v CartView) CanCheckout() bool { return !v.Empty() && !v.Blocked() }

// ShippingChoice is one delivery method at checkout.
type ShippingChoice struct {
	VersionID string
	Code      string
	// DestinationKind decides which fields the form asks for: a street address
	// or a convenience store. It comes from shipping_methods, so adding a
	// method with a new destination is a schema decision rather than a branch
	// somewhere in the checkout.
	DestinationKind string
	Name            string
	Carrier         string
	FeeCents        int64
	Free            bool
}

// Fee is what this method costs for the current subtotal. The free case is the
// template's to word — see CheckoutView.ShipsFree.
func (c ShippingChoice) Fee() string { return twd(c.FeeCents) }

// CheckoutView is the checkout form.
type CheckoutView struct {
	Cart     CartView
	Shipping []ShippingChoice
	Chosen   string // the selected shipping version id
	// What delivery ACTUALLY costs for the address that was typed, which the
	// method chooser could not know: it is a link, and the address is a form
	// below it. The order carries this figure, and a customer is never charged
	// it until they have seen it.
	QuotedShippingCents int64
	SurchargeCents      int64
	ZoneName            string
	// Destination is the chosen method's destination kind, decided by the
	// server. The form renders the half of the address it names, and there is
	// no field carrying it back: a submission cannot pick its own destination.
	Destination    string
	Address        CheckoutAddress
	Errors         map[string]string
	Invoice        CheckoutInvoice
	InvoiceChoices []InvoiceChoice
	PickupBrands   []PickupBrandChoice
	// SavedAddresses is the customer's address book, empty for a guest. The
	// book existed from the day the account pages shipped and the one page that
	// needed it did not read it, so every repeat customer retyped an address
	// they had already given us.
	SavedAddresses []SavedAddress
	// ChosenAddress is the saved address the form is filled from, so the
	// chooser can mark it. Empty means the fields were typed.
	ChosenAddress string
	// The coupon field and what it did, echoed back so the page explains the
	// number it is showing.
	CouponCode          string
	CouponApplied       string
	CouponDiscountCents int64
	CouponFreeShipping  bool
	Idempoten           string // the idempotency key this form carries
}

// InvoiceChoice is one option in the invoice-type radio group. It is built from
// the package that owns the rule rather than restated here, so the form and the
// validator cannot come to offer different things.
type InvoiceChoice struct {
	Value string
	Label string
}

// CheckoutAddress is the form's own values, echoed back on rejection so a
// visitor never retypes a form the server refused.
type CheckoutAddress struct {
	Email      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string

	Note string
}

// ToPickupPoint reports whether the chosen method delivers to a convenience
// store. Named for what it means rather than compared to a string in the
// template, so the two cannot drift apart.
func (v *CheckoutView) ToPickupPoint() bool { return v.Destination == "pickup_point" }

// PickupBrandChoice is one convenience-store chain the form offers.
type PickupBrandChoice struct {
	Value string
	Label string
}

// PickupBrandChoices is what any form collecting a 門市 offers.
//
// Built from PickupBrands and PickupBrandLabel rather than written out, so the
// checkout and the back office's correction form cannot come to offer different
// lists — and neither can offer one the validator refuses.
func PickupBrandChoices() []PickupBrandChoice {
	out := make([]PickupBrandChoice, 0, len(PickupBrands))
	for _, b := range PickupBrands {
		out = append(out, PickupBrandChoice{Value: b, Label: PickupBrandLabel(b)})
	}
	return out
}

// PickupBrands are the convenience-store chains a parcel may be sent to,
// in the order the form offers them.
//
// An allowlist, because the brand decides which carrier's manifest the parcel
// joins: free text there is a parcel that never leaves.
var PickupBrands = []string{"seven_eleven", "family_mart", "hi_life", "ok_mart"}

// PickupBrandLabel is what a customer reads. The switch has no silent default:
// a brand added to PickupBrands and not here would otherwise render as an empty
// label on the one control the customer has to choose from.
func PickupBrandLabel(code string) string {
	switch code {
	case "":
		// An erased or address-bound order. Rendering "" keeps Delivery.Line
		// total, where the panic below is for a brand that was stored and never
		// taught to this switch.
		return ""
	case "seven_eleven":
		return "7-ELEVEN"
	case "family_mart":
		return "全家 FamilyMart" // i18n-exempt: a brand's own name, already bilingual
	case "hi_life":
		return "萊爾富 Hi-Life" // i18n-exempt: a brand's own name, already bilingual
	case "ok_mart":
		return "OK mart"
	default:
		panic("cart: unknown pickup brand: " + code)
	}
}

// Delivery is where one order goes, as read back from order_private_data.
//
// It exists so the two pages that show an order — the customer's and the back
// office's — format the destination the same way. Each read the same columns
// and each built the same string, which worked until a destination that is not
// a street address arrived and both rendered a blank line.
type Delivery struct {
	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string
}

// IsPickup reports whether this order is collected from a convenience store.
//
// Decided by the store CODE, which is the column that identifies the
// destination — a brand alone identifies nothing, and a name alone is a label
// no carrier can route on.
func (d Delivery) IsPickup() bool { return d.PickupStoreCode != "" }

// Line is the destination as one line a person can read.
//
// An erased order has neither destination, and it renders as the empty string
// rather than as stray punctuation — which is what a page should show for a
// customer who asked to be forgotten.
func (d Delivery) Line() string {
	if d.IsPickup() {
		return PickupBrandLabel(d.PickupBrand) + " " + d.PickupStoreName +
			"(" + d.PickupStoreCode + ")"
	}
	return strings.TrimSpace(d.PostalCode + " " + d.City + d.District + d.Street)
}

// SavedAddress is one address from the customer's book, offered at checkout.
//
// It is a separate type from AccountAddress even though the fields match: that
// one is the account page's model and grows with what the account page needs,
// and a shared struct would make every change there a change to the checkout.
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

// Line is the address as one line, for the chooser.
func (a SavedAddress) Line() string {
	return strings.TrimSpace(a.PostalCode + " " + a.City + a.District + a.Street)
}

// DisplayLabel is the address's own name, or a stand-in. A customer who never
// named one still needs something to click.
func (a SavedAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

// CheckoutLink is this page's URL with one parameter changed.
//
// The chooser links have to carry the OTHER choice: a customer who picked 超商
// 取貨 and then a saved address must not be sent back to 宅配 by the second
// link. Built here rather than in the template so the two controls cannot come
// to disagree about which parameters exist.
func (v *CheckoutView) CheckoutLink(param, value string) string {
	q := url.Values{}
	if v.Chosen != "" {
		q.Set("ship", v.Chosen)
	}
	if v.ChosenAddress != "" {
		q.Set("address", v.ChosenAddress)
	}
	q.Set(param, value)
	return "/checkout?" + q.Encode()
}

// OffersTheAddressBook reports whether the chooser is worth rendering: a saved
// address is only relevant when the parcel is going to an address.
func (v *CheckoutView) OffersTheAddressBook() bool {
	return !v.ToPickupPoint() && len(v.SavedAddresses) > 0
}

// CheckoutMeta is the chrome view model for checkout.
func CheckoutMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCheckoutTitle)}
}

// Err returns the message for a field, or "".
func (v *CheckoutView) Err(field string) string { return v.Errors[field] }

// HasErr reports whether a field was rejected, which drives aria-invalid.
func (v *CheckoutView) HasErr(field string) bool { return v.Errors[field] != "" }

// Invalid is the aria-invalid value for a field.
func (v *CheckoutView) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

// AnyErrors reports whether the form was rejected at all.
func (v *CheckoutView) AnyErrors() bool { return len(v.Errors) > 0 }

// QuotedShipping is the figure the form carries back, as a string.
func (v *CheckoutView) QuotedShipping() string {
	return strconv.FormatInt(v.QuotedShippingCents, 10)
}

// HasSurcharge reports whether this address costs extra to reach.
func (v *CheckoutView) HasSurcharge() bool { return v.SurchargeCents > 0 }

// Surcharge is the extra, for the summary line.
func (v *CheckoutView) Surcharge() string { return twd(v.SurchargeCents) }

// ShippingFeeCents is what the chosen method charges, for the total.
//
// The re-quoted figure wins once it exists: it is the one priced against the
// address the customer typed, and the chooser's number is a mainland estimate.
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

// BaseShippingCents is the delivery charge WITHOUT the 離島 surcharge.
//
// The fee row and the surcharge row are two lines on the summary, deliberately
// — the template's own comment says 「運費 NT$280」 for an order the customer
// expected to pay NT$80 for reads as a mistake. But the fee row printed
// ShippingFeeCents, which is the quote's TOTAL and already includes the
// surcharge, so the two lines showed 280 and 200 while the total added 280
// once: the exact reading that comment exists to prevent, produced by the code
// underneath it.
func (v *CheckoutView) BaseShippingCents() int64 {
	return v.ShippingFeeCents() - v.SurchargeCents
}

// ShippingText is the chosen method's fee as money, after any free-shipping
// coupon. A coupon that zeroes the fee must read as 免運 here and not as an
// unexplained smaller total below.
func (v *CheckoutView) ShippingText() string { return twd(v.BaseShippingCents()) }

// ShipsFree reports whether delivery costs nothing, so the TEMPLATE can say so
// in the visitor's language.
//
// The word used to be returned from here, which put a Chinese string in a view
// model that has no locale — and left i18n.KeyFreeShipping translated and
// unrendered. Deciding here and wording there is the split the rest of the
// chrome already follows.
func (v *CheckoutView) ShipsFree() bool {
	return v.CouponFreeShipping || v.BaseShippingCents() == 0
}

// Total is what the visitor will owe.
func (v *CheckoutView) Total() string {
	return twd(v.TotalCents())
}

// TotalCents is what the customer will be charged, discount and free shipping
// included.
//
// The same arithmetic PlaceOrder does. It has to be: a checkout page showing
// one figure while the order is written at another is the bug a customer
// notices on their card statement, and the one they never trust the shop about
// again.
// A 免運 coupon zeroes the shop's BASE RATE and never the 離島 surcharge, which
// is what a carrier charges to cross the water. This used to zero the whole
// shipping figure — surcharge included — while cart.priceOrder deliberately
// kept it, so the page promised subtotal-discount and the order was written at
// subtotal+surcharge-discount. The customer met the difference one page later,
// on /orders/{number}/pay, as an unexplained jump from the total they had just
// agreed to.
//
// The rule is stated in shipping_version_zones' own COMMENT ON COLUMN — 「免運
// covers the base rate the shop advertises, never the 離島 surcharge a carrier
// charges on top of it」 — and this was the one of its three readers that had
// not learned it.
func (v *CheckoutView) TotalCents() int64 {
	shipping := v.BaseShippingCents()
	if v.CouponFreeShipping {
		shipping = 0
	}
	return v.Cart.SubtotalCents + shipping + v.SurchargeCents - v.CouponDiscountCents
}

// OrderLine is one line on the confirmation page.
type OrderLine struct {
	SKU       string
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

// UnitPrice is the price paid per unit.
func (l OrderLine) UnitPrice() string { return twd(l.UnitCents) }

// LineTotal is what the line came to.
func (l OrderLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

// QuantityText is how many were ordered.
func (l OrderLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// OrderEvent is one entry in an order's history, as the CUSTOMER sees it.
//
// There is no actor field on purpose. An order page is reachable by anyone
// holding the number, and a timeline naming the staff member who picked it
// hands out employee identities with it.
type OrderEvent struct {
	Kind string
	Note string
	At   string
}

// LabelKey names the entry's message. The kinds are order_events' CHECK, so an
// unknown one is a schema change nobody carried through here — which must be
// loud rather than rendered blank.
//
// A KEY rather than a string, because this method has no request to read a
// locale from and taking one would put a context parameter on a pure mapping.
// The template renders it.
func (e OrderEvent) LabelKey() i18n.Key {
	switch e.Kind {
	case "placed":
		return i18n.KeyStatusPlaced
	case "paid":
		return i18n.KeyStatusPaid
	case "picking":
		return i18n.KeyStatusPicking
	case "shipped":
		return i18n.KeyStatusShipped
	case "in_transit":
		return i18n.KeyStatusInTransit
	case "delivered":
		return i18n.KeyStatusDelivered
	case "completed":
		return i18n.KeyStatusCompleted
	case "cancelled":
		return i18n.KeyStatusCancelled
	case "refunded":
		return i18n.KeyStatusRefunded
	default:
		panic("pages: no label for order event kind " + e.Kind)
	}
}

// OrderShipment is a dispatch the customer can follow.
type OrderShipment struct {
	Carrier     string
	Tracking    string
	ShippedAt   string
	DeliveredAt string
}

// Delivered reports whether this shipment has arrived.
func (s OrderShipment) Delivered() bool { return s.DeliveredAt != "" }

// CheckoutInvoice carries the invoice choice back into a refused form.
type CheckoutInvoice struct {
	Type    string
	Carrier string
	TaxID   string
}

// Is reports whether this is the chosen type, for the radio group. An empty
// choice reads as the member carrier, which is the default the form opens on.
func (i CheckoutInvoice) Is(t string) bool {
	if i.Type == "" {
		return t == "member_carrier"
	}
	return i.Type == t
}

// OrderView is the confirmation page.
type OrderView struct {
	Number       string
	Status       string
	Email        string
	ShippingName string
	// DeliveryTo is where the order goes, already formatted. The confirmation
	// named the METHOD and never the destination, which is fine for 宅配 —
	// the customer typed the address a moment ago — and wrong for 超商取貨,
	// where "which store did I pick?" is the whole question this page is read
	// to answer.
	DeliveryTo    string
	PlacedAt      string
	Lines         []OrderLine
	SubtotalCents int64
	ShippingCents int64
	DiscountCents int64
	// DiscountReason is which coupon, as "CODE · description", or "" when the
	// order had no coupon. Joined at read time rather than snapshotted here:
	// coupons.code is never updated and the FK is ON DELETE RESTRICT.
	DiscountReason string
	TaxCents       int64
	Timeline       []OrderEvent
	Shipments      []OrderShipment
	// Cancelled is the one-shot notice a successful cancellation redirects
	// with, so a reload does not resubmit the form behind it.
	Cancelled bool
	// Committed is whether the order is funded — paid, or covered by store
	// credit. NOT the same as a status: an order stays 'pending' between the
	// capture and the shop picking it.
	Committed bool
}

// CanCancel reports whether the customer may still call this order off.
//
// Unpaid and not yet being picked. A paid order is not cancelled but refunded,
// and that is the shop's decision — the button is absent rather than present
// and refused, because a control that always says no is worse than no control.
//
// The database decides the same thing in CancelOrderByCustomer's WHERE clause;
// this only decides whether to offer the button.
func (v *OrderView) CanCancel() bool {
	return v.Status == "pending" && !v.Committed
}

// OrderMeta is the chrome view model for a placed order.
func OrderMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderMeta), number)}
}

// Subtotal is what the lines came to before shipping.
func (v *OrderView) Subtotal() string { return twd(v.SubtotalCents) }

// Shipping is what delivery cost.
func (v *OrderView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

// Discounted reports whether anything came off this order.
//
// The discount used to be absent from the order page entirely: subtotal plus
// shipping did not equal the total, and nothing accounted for the difference. A
// customer reading their own receipt could not tell whether they had been
// overcharged.
func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v *OrderView) Discount() string { return "-" + twd(v.DiscountCents) }

// Total is what the order came to.
func (v *OrderView) Total() string {
	return twd(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

// CanRequestReturn reports whether the order has reached a state where sending
// something back is the right action.
//
// Anything before 'shipped' has not left the warehouse, and the answer there is
// cancellation, not a return — return_within_shipment would refuse every line
// anyway, so offering the link would be offering a form that cannot succeed.
func (v *OrderView) CanRequestReturn() bool {
	switch v.Status {
	case "shipped", "delivered", "completed":
		return true
	default:
		return false
	}
}

// AwaitingPayment reports whether the order is still waiting to be paid, which
// is the state every order is in the moment it is placed.
func (v *OrderView) AwaitingPayment() bool { return v.Status == "pending" }

// HasCoupon reports whether a coupon is applied to this checkout.
func (v *CheckoutView) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

// CouponDiscount is what it takes off, as a negative figure — a discount shown
// as a positive number in a column of positives reads as another charge.
func (v *CheckoutView) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }
