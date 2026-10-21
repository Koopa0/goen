package buyer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/layouts"
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
	TotalCents     int64
	CreditCents    int64
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

// Total states the database's order_totals figure rather than re-adding the
// subtotal, discount and delivery, so no page can sum them differently.
func (v *OrderView) Total() string { return twd(v.TotalCents) }

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
	return order.AwaitingPayment(v.Status, v.Committed, v.OwedCents)
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
