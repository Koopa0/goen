package pages

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// CartLine is one line in the cart.
type CartLine struct {
	VariantID    string
	Slug         string
	Name         string
	Brand        string
	SKU          string
	Label        string
	UnitCents    int64
	CompareCents int64
	Quantity     int32
	Available    int32

	// Unavailable is a line the catalogue can no longer honour at all; Short is
	// one where fewer remain than the cart asks for. Both block checkout.
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

// PricedQuantityText is how many this line is priced for, which differs from
// what the cart holds exactly when the shelf cannot meet it.
func (l CartLine) PricedQuantityText() string {
	return strconv.FormatInt(int64(l.effectiveQuantity()), 10)
}

// QuantityText is how many the cart holds.
func (l CartLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// AvailableText is how many remain.
func (l CartLine) AvailableText() string { return strconv.FormatInt(int64(l.Available), 10) }

// MaxQuantity bounds the line's quantity input to what can be supplied.
func (l CartLine) MaxQuantity() string {
	n := max(min(l.Available, 999), 1)
	return strconv.FormatInt(int64(n), 10)
}

// HasImage reports whether the thumbnail has an image.
func (l CartLine) HasImage() bool { return l.ImageURL != "" }

// CartView is the cart page.
type CartView struct {
	Lines         []CartLine
	SubtotalCents int64
	ItemCount     int64

	ReorderAdded   int
	ReorderSkipped int
}

// FromReorder reports whether this page is showing the result of a reorder.
func (v CartView) FromReorder() bool { return v.ReorderAdded > 0 || v.ReorderSkipped > 0 }

// ReorderText is what the reorder came to, in a sentence.
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
func CartMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyCart)}
}

// Empty reports whether the cart holds nothing.
func (v CartView) Empty() bool { return len(v.Lines) == 0 }

// Subtotal is the money the lines add up to.
func (v CartView) Subtotal() string { return twd(v.SubtotalCents) }

// ItemCountText is how many units are in the cart.
func (v CartView) ItemCountText() string { return strconv.FormatInt(v.ItemCount, 10) }

// Blocked reports whether any line stops checkout.
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
	VersionID       string
	Code            string
	DestinationKind string
	Name            string
	Carrier         string
	FeeCents        int64
	Free            bool
}

// Fee is what this method costs for the current subtotal.
func (c ShippingChoice) Fee() string { return twd(c.FeeCents) }

// CheckoutView is the checkout form.
type CheckoutView struct {
	Cart                CartView
	Shipping            []ShippingChoice
	Chosen              string
	QuotedShippingCents int64
	SurchargeCents      int64
	// Repriced is a cart/price/credit quote the customer has not seen yet. Not an
	// Errors entry: nothing they typed was wrong. Rendered as a notice and still
	// a 422 — nobody is charged for a quote they did not confirm.
	Repriced string
	// CreditChanged is the refreshed store-credit balance after it moved during
	// placement. Like Repriced, it is a notice rather than a field error: the
	// customer must see the new figure before submitting again.
	CreditChanged string
	ZoneName      string
	// Destination is decided by the server; no field carries it back.
	Destination          string
	Address              CheckoutAddress
	Errors               map[string]string
	Invoice              CheckoutInvoice
	InvoiceChoices       []InvoiceChoice
	PickupBrands         []PickupBrandChoice
	SavedAddresses       []SavedAddress
	ChosenAddress        string
	CouponCode           string
	CouponApplied        string
	CouponDiscountCents  int64
	CouponFreeShipping   bool
	AvailableCreditCents int64
	// QuoteID is the opaque identity of every commercial fact rendered below.
	// The cart package creates it; the page only carries it back unchanged.
	QuoteID        string
	IdempotencyKey string
}

// checkoutFieldHint tells a browser what one helper-rendered checkout control
// contains and, where its type is not enough, which keyboard to open.
type checkoutFieldHint struct {
	Autocomplete   string
	InputMode      string
	AutoCapitalize string
	SpellCheck     string
}

// checkoutFieldHints is keyed by the field's own form name. Every
// helper-rendered control must make an explicit autofill decision.
//
// Do not add enterkeyhint here: Enter in any checkout field places the order,
// so a "next" hint would label a key that charges the customer.
var checkoutFieldHints = map[string]checkoutFieldHint{
	"email":       {Autocomplete: "email"},
	"name":        {Autocomplete: "name"},
	"phone":       {Autocomplete: "tel"},
	"postal_code": {Autocomplete: "postal-code", InputMode: "numeric"},
	"city":        {Autocomplete: "address-level1"},
	"district":    {Autocomplete: "address-level2"},
	// A convenience-store code is not an address. It may begin with a letter,
	// so a numeric keyboard would make valid stores unreachable.
	"pickup_store_code": {
		Autocomplete: "off", AutoCapitalize: "characters", SpellCheck: "false",
	},
}

func checkoutHintsFor(name string) checkoutFieldHint { return checkoutFieldHints[name] }

// InvoiceChoice is one option in the invoice-type radio group.
type InvoiceChoice struct {
	Value invoice.Preference
	Label string
}

// CheckoutAddress is the form's own values, echoed back on rejection.
type CheckoutAddress struct {
	Email      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     pickup.Brand
	PickupStoreCode string
	PickupStoreName string

	Note string
}

// ToPickupPoint reports whether the chosen method delivers to a convenience store.
func (v *CheckoutView) ToPickupPoint() bool { return v.Destination == "pickup_point" }

// PickupBrandChoice is one convenience-store chain the form offers.
type PickupBrandChoice struct {
	Value pickup.Brand
	Label string
}

// PickupBrandChoices is what any form collecting a pickup store offers.
func PickupBrandChoices() []PickupBrandChoice {
	brands := pickup.Offered()
	out := make([]PickupBrandChoice, 0, len(brands))
	for _, b := range brands {
		out = append(out, PickupBrandChoice{Value: b, Label: pickupBrandLabel(b)})
	}
	return out
}

// pickupBrandLabel is what a customer reads.
func pickupBrandLabel(code pickup.Brand) string {
	switch code {
	case "":
		return ""
	case pickup.SevenEleven:
		return "7-ELEVEN"
	case pickup.FamilyMart:
		return "全家 FamilyMart" // i18n-exempt: a brand's own name, already bilingual
	case pickup.HiLife:
		return "萊爾富 Hi-Life" // i18n-exempt: a brand's own name, already bilingual
	case pickup.OKMart:
		return "OK mart"
	default:
		panic("pages: unknown pickup brand: " + string(code))
	}
}

// Delivery is where one order goes, as read back from order_private_data.
type Delivery struct {
	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     pickup.Brand
	PickupStoreCode string
	PickupStoreName string
}

// IsPickup reports whether this order is collected from a convenience store.
func (d Delivery) IsPickup() bool { return d.PickupStoreCode != "" }

// Line is the destination as one line a person can read.
func (d Delivery) Line() string {
	if d.IsPickup() {
		return pickupBrandLabel(d.PickupBrand) + " " + d.PickupStoreName +
			"(" + d.PickupStoreCode + ")"
	}
	return strings.TrimSpace(d.PostalCode + " " + d.City + d.District + d.Street)
}

// SavedAddress is one address from the customer's book, offered at checkout.
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

// DisplayLabel is the address's own name, or a stand-in.
func (a SavedAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

// OffersTheAddressBook reports whether the chooser is worth rendering.
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

// HasSurcharge reports whether this address costs extra to reach.
func (v *CheckoutView) HasSurcharge() bool { return v.SurchargeCents > 0 }

// Surcharge is the extra, for the summary line.
func (v *CheckoutView) Surcharge() string { return twd(v.SurchargeCents) }

// ShippingFeeCents is what the chosen method charges, for the total.
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

// BaseShippingCents is the delivery charge less the surcharge ShippingFeeCents carries.
func (v *CheckoutView) BaseShippingCents() int64 {
	return v.ShippingFeeCents() - v.SurchargeCents
}

// ShippingText is the chosen method's fee as money, after any free-delivery coupon.
func (v *CheckoutView) ShippingText() string { return twd(v.BaseShippingCents()) }

// ShipsFree reports whether delivery costs nothing.
func (v *CheckoutView) ShipsFree() bool {
	return v.CouponFreeShipping || v.BaseShippingCents() == 0
}

// ChargedShippingCents is the delivery amount PlaceOrder will store. A
// free-shipping coupon removes the base rate but deliberately not a remote-zone
// surcharge.
func (v *CheckoutView) ChargedShippingCents() int64 {
	if v.CouponFreeShipping {
		return v.SurchargeCents
	}
	return v.ShippingFeeCents()
}

// Total is what the visitor will owe.
func (v *CheckoutView) Total() string {
	return twd(v.TotalCents())
}

// GrossCents is the order total before store credit.
func (v *CheckoutView) GrossCents() int64 {
	return v.Cart.SubtotalCents + v.ChargedShippingCents() - v.CouponDiscountCents
}

// CreditCents is the store credit the displayed quote applies. An increase in
// balance after rendering does not silently spend more; a decrease that cannot
// fund this amount makes placement refresh the quote.
func (v *CheckoutView) CreditCents() int64 {
	return min(v.AvailableCreditCents, v.GrossCents())
}

// UsesCredit reports whether the checkout summary needs a credit line.
func (v *CheckoutView) UsesCredit() bool { return v.CreditCents() > 0 }

// Credit is the applied store credit as a negative money figure.
func (v *CheckoutView) Credit() string { return "-" + twd(v.CreditCents()) }

// TotalCents is what remains payable after the exact displayed credit spend.
func (v *CheckoutView) TotalCents() int64 { return v.GrossCents() - v.CreditCents() }

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

// OrderEvent is one entry in an order's history, as the customer sees it. It carries
// no actor: anyone holding the order number can reach this page.
type OrderEvent struct {
	Kind string
	Note string
	At   string
}

// LabelKey names the entry's message.
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
	Type        invoice.Preference
	Carrier     string
	CompanyName string
	TaxID       string
}

// Is reports whether this is the chosen type.
func (i CheckoutInvoice) Is(t invoice.Preference) bool { return i.Chosen() == t }

// Chosen is the type in force, which is the default until somebody picks one.
func (i CheckoutInvoice) Chosen() invoice.Preference {
	if i.Type == "" {
		return invoice.PreferenceMember
	}
	return i.Type
}

// NeedsCarrier reports whether the 載具 field applies. It and NeedsTaxID decide
// which half of the form exists: rendering both would leave required promising
// something the server will not demand.
func (i CheckoutInvoice) NeedsCarrier() bool { return i.Chosen().NeedsCarrier() }

// NeedsTaxID reports whether the 統編 field applies.
func (i CheckoutInvoice) NeedsTaxID() bool { return i.Chosen().NeedsTaxID() }

// OrderView is the confirmation page.
type OrderView struct {
	Number         string
	Status         FulfillmentStatus
	Email          string
	ShippingName   string
	DeliveryTo     string
	PlacedAt       string
	Lines          []OrderLine
	SubtotalCents  int64
	ShippingCents  int64
	DiscountCents  int64
	DiscountReason string
	CreditCents    int64
	TaxCents       int64
	Timeline       []OrderEvent
	Shipments      []OrderShipment
	Cancelled      bool
	Committed      bool
	// OwedCents is what is left to pay: the total less the store credit spent on it.
	OwedCents int64
	// ShowWarrantyLink is set when a signed-in account owns the order. Guest-token
	// viewers can read the page but must not see account-only registration.
	ShowWarrantyLink bool
}

// CanCancel reports whether the customer may still call this order off.
func (v *OrderView) CanCancel() bool {
	return v.Status == FulfillmentPending && !v.Committed
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
func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v *OrderView) Discount() string { return "-" + twd(v.DiscountCents) }

// Total is what the order came to.
func (v *OrderView) Total() string {
	return twd(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

// UsedCredit reports whether to show the store-credit row. Without it the
// summary and the payment page state different figures, since the credit is
// debited in the order's own transaction.
func (v *OrderView) UsedCredit() bool { return v.CreditCents > 0 }

// Credit is what the store credit took off, as a negative figure.
func (v *OrderView) Credit() string { return "-" + twd(v.CreditCents) }

// CanRequestReturn reports whether the goods have left the warehouse.
func (v *OrderView) CanRequestReturn() bool {
	switch v.Status {
	case FulfillmentShipped, FulfillmentDelivered, FulfillmentCompleted:
		return true
	default:
		return false
	}
}

// CanRegisterWarranty reports whether to offer the form: both statuses that end a delivery.
func (v *OrderView) CanRegisterWarranty() bool {
	switch v.Status {
	case FulfillmentDelivered, FulfillmentCompleted:
		return true
	default:
		return false
	}
}

// WarrantyLink is where that form lives.
func (v *OrderView) WarrantyLink() string { return "/account/warranty/" + v.Number }

// AwaitingPayment reports whether the order is waiting to be paid.
func (v *OrderView) AwaitingPayment() bool {
	return awaitingPayment(v.Status, v.Committed, v.OwedCents)
}

// HasCoupon reports whether a coupon is applied to this checkout.
func (v *CheckoutView) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

// CouponDiscount is what it takes off, as a negative figure.
func (v *CheckoutView) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }
