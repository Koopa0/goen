package outbox

import (
	"time"

	"github.com/google/uuid"
)

// The payload of every topic. Each one is the JSON an outbox row stores, so a
// field's tag is a stored contract: a row queued before a rename decodes to a
// zero value. wire_test.go holds the stored form of each.

type OrderPlaced struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	TotalCents  int64  `json:"total_cents"`
	// OwedCents is what is still payable after store credit. Zero is a value,
	// not an absence: a fully funded order writes 0 and gets the letter with
	// nothing to pay, so the field is never omitted.
	OwedCents int64           `json:"owed_cents"`
	Snapshot  *PlacedSnapshot `json:"snapshot,omitempty"`
}

// PlacedSnapshot records the terms when checkout commits, rather than reading
// a changed order or catalogue when the outbox finally sends its confirmation.
type PlacedSnapshot struct {
	Lines          []PlacedLine `json:"lines"`
	SubtotalCents  int64        `json:"subtotal_cents"`
	ShippingCents  int64        `json:"shipping_cents"`
	DiscountCents  int64        `json:"discount_cents"`
	DiscountReason string       `json:"discount_reason"`
	CreditCents    int64        `json:"credit_cents"`
	ShippingName   string       `json:"shipping_name"`
	DeliveryTo     string       `json:"delivery_to"`
	Phone          string       `json:"phone"`
	DeliveryNote   string       `json:"delivery_note"`
	HoldUntil      time.Time    `json:"hold_until"`
	StartBy        time.Time    `json:"start_by"`
}

type PlacedLine struct {
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	UnitCents int64  `json:"unit_cents"`
	Quantity  int32  `json:"quantity"`
}

type OrderPaid struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AmountCents int64  `json:"amount_cents"`
	// Card is "Visa •••• 4242" or empty. It is empty when Stripe could not be
	// asked for the charge; the mail then says nothing about the card rather
	// than inventing one.
	Card string `json:"card"`
}

type OrderShipped struct {
	Locale      string `json:"locale"`
	OrderNumber string `json:"order_number"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Carrier     string `json:"carrier"`
	Tracking    string `json:"tracking"`
	// Pickup is a convenience-store order, whose parcel goes to a store.
	Pickup bool `json:"pickup"`
}

type TerminalKind string

const (
	TerminalCancelledByCustomer        TerminalKind = "cancelled_by_customer"
	TerminalCancelledByStaff           TerminalKind = "cancelled_by_staff"
	TerminalCancelledByPaymentDeadline TerminalKind = "cancelled_by_payment_deadline"
	TerminalDelivered                  TerminalKind = "delivered"
	TerminalCollected                  TerminalKind = "collected"
)

// OrderTerminal is what an order.terminal message carries: the order and the
// fact, never an address.
type OrderTerminal struct {
	OrderID uuid.UUID    `json:"order_id"`
	Kind    TerminalKind `json:"kind"`
	// Refunded: money taken from the customer has been or will be returned, so
	// the mail must not say nothing was charged. Set from what the cancelling
	// transaction reads, including a payment that may still land. Absent from an
	// older row, it reads false.
	Refunded bool `json:"refunded"`
}

// OrderPartRefunded is what an order.part_refunded message carries: the order
// and the refund of the units it could not send, never an address, a name or
// an amount. Everything the letter says is read at delivery.
type OrderPartRefunded struct {
	OrderID         uuid.UUID `json:"order_id"`
	ReturnRequestID uuid.UUID `json:"return_request_id"`
}

type RestockNotice struct {
	Locale      string `json:"locale"`
	Email       string `json:"email"`
	ProductName string `json:"product_name"`
	Slug        string `json:"slug"`
	SKU         string `json:"sku"`
}

// PasswordResetRequest is a queued forgotten-password request. It names the
// account by id and never by address, and names none when the address has no
// account: the request is queued either way, so asking about an address costs
// the same whether or not it belongs to somebody.
type PasswordResetRequest struct {
	UserID string `json:"user_id"`
	Locale string `json:"locale"`
}

// PasswordReset is what a reset message needs. The token travels in the payload
// and lives in outbox_messages until the row is swept.
type PasswordReset struct {
	Locale string `json:"locale"`
	// Tagged, like every other payload here: erase_user matches the exact
	// recipient field, so a row that names the address under another key is
	// not erased.
	Email string `json:"email"`
	Token string `json:"token"`
}

// AccountRegistration is a queued registration. It names the account by id and
// never by address: the new one, or the one that already held the address. Next
// is the same-site path the registrant was headed for, carried to the link.
type AccountRegistration struct {
	UserID  string `json:"user_id"`
	Created bool   `json:"created"`
	Locale  string `json:"locale"`
	Next    string `json:"next"`
}

// AddressVerify is what an account.email_verify message carries. The address is
// in the payload as well as being the recipient, because a CHANGE sends to the
// new address while the account still holds the old one.
type AddressVerify struct {
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
	// Registration is a link that completes a registration. It lands on a page
	// that asks for the password chosen at registration, because the mailbox
	// alone does not say who chose it.
	Registration bool `json:"registration,omitempty"`
	// Next is the same-site path a registration was headed for. The link
	// carries it and the page it lands on checks it again.
	Next string `json:"next,omitempty"`
}

type NewsletterConfirm struct {
	Locale string `json:"locale"`
	Email  string `json:"email"`
	Token  string `json:"token"`
}

// NewsletterWelcome is what a newsletter.welcome message carries. Its
// unsubscribe token never expires, because an email sent a year ago still has to
// take somebody off the list.
type NewsletterWelcome struct {
	Locale           string `json:"locale"`
	Email            string `json:"email"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

// NewsletterIssue is one copy of one newsletter, addressed to one person. The
// subject and body are the shop's own words and go out as written; only the
// unsubscribe footer is translated.
type NewsletterIssue struct {
	Locale           string `json:"locale"`
	Email            string `json:"email"`
	Subject          string `json:"subject"`
	Body             string `json:"body"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

// StaffInvitation identifies a newly invited account without retaining its PII.
type StaffInvitation struct {
	UserID string `json:"user_id"`
	Locale string `json:"locale"`
}

// StaffEnrolment carries the code that finishes a staff member's second-factor
// enrolment to the account's address.
type StaffEnrolment struct {
	Locale string `json:"locale"`
	// Tagged "email" so erase_user's sweep of an erased address finds it.
	Email string `json:"email"`
	Code  string `json:"code"`
}

// StaffFactorFailure tells a staff account that a wrong second-factor code was
// entered after its password: the first since a right one, or the one that
// locked the factor.
type StaffFactorFailure struct {
	Locale string `json:"locale"`
	// Tagged "email" so erase_user's sweep of an erased address finds it.
	Email string `json:"email"`
	// Locked is the letter for the code that locked the factor.
	Locked bool `json:"locked"`
	// Limit is how many wrong codes in a row lock it.
	Limit int `json:"limit"`
}

// InvoiceDue is an order whose sale became final, so its 統一發票 is owed now:
// 營業稅法 §32's 時限表 invoices a prepaid sale when the money arrives, not at
// dispatch.
type InvoiceDue struct {
	OrderNumber string `json:"order_number"`
	// Trigger is the provider event that captured the payment, the payment staff
	// attributed, or the checkout that store credit paid in full. It is the
	// system claim's request id.
	Trigger string `json:"trigger"`
}

// InvoiceVoidDue is an order cancelled after its 統一發票 was owed: store
// credit paid it in full at checkout, and its customer, or staff on the
// customer's word, cancelled it before packing.
type InvoiceVoidDue struct {
	OrderNumber string `json:"order_number"`
	// Trigger is the cancellation, and the system void's request id.
	Trigger string `json:"trigger"`
}
