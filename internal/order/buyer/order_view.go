package buyer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// OrderShipment is one parcel. The right to cancel and the warranty of the lines in it count from its own delivery.
type OrderShipment struct {
	Carrier   shipping.Carrier
	Tracking  string
	ShippedAt time.Time
	// DeliveredAt is zero until the parcel arrives, or is collected from a store.
	DeliveredAt time.Time
	// RescissionEnds and GoodwillEnds are the last day of the right to cancel and the last day of unused
	// returns, as the database reads them; zero until the parcel is delivered.
	RescissionEnds time.Time
	GoodwillEnds   time.Time
	// RescissionDays is how long the right to cancel lasts once the parcel arrives, as the database states it.
	RescissionDays int64
	// Lines are the units of each line that went in this parcel.
	Lines []OrderLine
}

func (s OrderShipment) TrackURL() string { return s.Carrier.TrackingURL(s.Tracking) }

func (s OrderShipment) Delivered() bool { return !s.DeliveredAt.IsZero() }

type OrderView struct {
	Number       string
	Status       order.FulfillmentStatus
	Email        string
	ShippingName string
	DeliveryTo   string
	PlacedAt     time.Time
	// Now is the moment the page is read; the days left and the grids count from it.
	Now time.Time
	// HoldFrom and HoldUntil are when the first stored reservation was taken and when the earliest stored
	// reservation expires, zero without rows; Checkout Session expiry is bound to HoldUntil.
	HoldFrom, HoldUntil time.Time
	// Pickup is set for an order collected from a store, where delivery reads as collection.
	Pickup bool
	// Lines are the lines as bought; Unshipped are the units no parcel carries yet.
	Lines         []OrderLine
	Unshipped     []OrderLine
	Returned      *OrderReturned
	SubtotalCents int64
	ShippingCents int64
	DiscountCents int64
	Coupon        coupon.Redeemed
	TotalCents    int64
	CreditCents   int64
	Timeline      []OrderEvent
	Shipments     []OrderShipment
	Invoice       *OrderInvoice
	Cancelled     bool
	Committed     bool
	OwedCents     int64
	// OpenReturn is a return request nobody has decided, which is what a second request is refused for; HasReturn
	// is any request the buyer made, decided or not.
	OpenReturn, HasReturn bool
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

// PaidByCredit is true for an order store credit paid in full: it owes nothing and has no payment.
func (v *OrderView) PaidByCredit() bool { return v.CreditCents > 0 && v.OwedCents == 0 }

// CancelVoidsInvoice reports whether cancelling voids the order's 統一發票:
// store credit paid it in full, and the invoice was owed then.
func (v *OrderView) CancelVoidsInvoice() bool { return v.PaidByCredit() }

func OrderMeta(ctx context.Context, number string) frame.Page {
	return frame.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderMeta), number)}
}

func (v *OrderView) Subtotal() string { return twd(v.SubtotalCents) }

func (v *OrderView) Shipping(ctx context.Context) string {
	if v.ShippingCents == 0 {
		return i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return twd(v.ShippingCents)
}

func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

// ListsDiscount is true while the summary has a discount row: a free-delivery code has one at nothing.
func (v *OrderView) ListsDiscount() bool { return v.Discounted() || v.Coupon.Used() }

func (v *OrderView) Discount() string {
	if v.DiscountCents == 0 {
		return twd(0)
	}
	return "-" + twd(v.DiscountCents)
}

// Total states the database's order_totals figure rather than re-adding the
// subtotal, discount and delivery, so no page can sum them differently.
func (v *OrderView) Total() string { return twd(v.TotalCents) }

// UsedCredit gates the credit row: without it the summary and the payment page
// state different figures, since the credit is debited in the order's own transaction.
func (v *OrderView) UsedCredit() bool { return v.CreditCents > 0 }

func (v *OrderView) Credit() string { return "-" + twd(v.CreditCents) }

// CanRequestReturn is offered from shipment, as the POST and return_within_shipment allow; the days count from
// receipt. A request nobody has decided refuses another.
func (v *OrderView) CanRequestReturn() bool {
	if v.Returned != nil || v.OpenReturn {
		return false
	}
	switch v.Status {
	case order.FulfillmentShipped, order.FulfillmentDelivered, order.FulfillmentCompleted:
		return true
	default:
		return false
	}
}

// ViewsReturn is true where the order has no request to make but has one to read: the return page is the only
// place its outcome is shown.
func (v *OrderView) ViewsReturn() bool { return !v.CanRequestReturn() && (v.OpenReturn || v.HasReturn) }

// AwaitsDelivery is true while a return could be requested but no parcel has arrived to count its days from.
func (v *OrderView) AwaitsDelivery() bool {
	return v.CanRequestReturn() && len(v.Shipments) > 0 && !slices.ContainsFunc(v.Shipments, OrderShipment.Delivered)
}

// ReturnAfterDelivery is what the return button says about the days, until a parcel has been delivered.
func (v *OrderView) ReturnAfterDelivery(ctx context.Context) string {
	if !v.AwaitsDelivery() {
		return ""
	}
	days := v.Shipments[0].RescissionDays
	return i18n.Count(ctx, i18n.KeyOrderReturnAfterDelivery, days, days)
}

func (v *OrderView) WarrantyLink() string { return "/account/warranty/" + v.Number }

func (v *OrderView) AwaitingPayment() bool {
	return order.AwaitingPayment(v.Status, v.Committed, v.OwedCents)
}

// OffersPayment is true while the side panel leads with paying. Just back from paying, an order that still reads
// unpaid is not offered a second payment.
func (v *OrderView) OffersPayment() bool {
	return v.AwaitingPayment() && v.PaymentRefreshURL == "" && !v.PaymentConfirmationPending
}

// HoldWindow is the stock hold in the pay page's own words, read from the same stored span. This page cannot tell
// whether a payment is already open, so while one could still start it names the deadline to start paying, as the
// order email does: a shopper whose payment is open reads an earlier time than theirs, never a later one.
func (v *OrderView) HoldWindow(ctx context.Context) (lead, note string, ok bool) {
	h := payment.PayHold{PlacedAt: v.HoldFrom, Until: v.HoldUntil}
	if startBy := payment.StartBy(v.HoldUntil); v.PaymentsEnabled && v.Now.Before(startBy) {
		h.StartBy = startBy
	}
	return h.Window(ctx)
}

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
	IssuedOn    shoptime.Date
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
		panic("buyer: no label for invoice type " + string(i.Type))
	}
}

func maskMobileBarcode(code string) string {
	if len(code) <= 3 {
		return code
	}
	return code[:1] + strings.Repeat("*", len(code)-3) + code[len(code)-2:]
}
