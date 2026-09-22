package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

const refundRequestKeyTag = "goen_request_key"

// refundEvents are Stripe refund lifecycle webhooks goen reconciles.
var refundEvents = map[stripe.EventType]bool{
	"refund.created": true,
	"refund.updated": true,
	"refund.failed":  true,
}

// chargeRefundedEvent is a charge-level signal that money moved back.
const chargeRefundedEvent = "charge.refunded"

// RefundStatus is the provider state in goen's refund vocabulary.
type RefundStatus string

const (
	RefundPending        RefundStatus = "pending"
	RefundRequiresAction RefundStatus = "requires_action"
	RefundSucceeded      RefundStatus = "succeeded"
	RefundFailed         RefundStatus = "failed"
	RefundCancelled      RefundStatus = "cancelled"
)

func refundStatus(status stripe.RefundStatus) (RefundStatus, bool) {
	switch status {
	case stripe.RefundStatusPending:
		return RefundPending, true
	case stripe.RefundStatusRequiresAction:
		return RefundRequiresAction, true
	case stripe.RefundStatusSucceeded:
		return RefundSucceeded, true
	case stripe.RefundStatusFailed:
		return RefundFailed, true
	case stripe.RefundStatusCanceled:
		// Stripe uses one l; the database uses the shop's cancelled state.
		return RefundCancelled, true
	default:
		return "", false
	}
}

// ProviderRefund is a verified refund object from a webhook payload.
type ProviderRefund struct {
	ProviderRef       string
	PaymentIntentRef  string
	ChargeRef         string
	AmountCents       int64
	Currency          string
	Status            RefundStatus
	RequestKey        string
	FailureReason     string
	ProviderUpdatedAt int64
}

// RefundFrom reads a refund.* event into a [ProviderRefund].
func RefundFrom(ev *stripe.Event) (ProviderRefund, bool) {
	if ev == nil || ev.Data == nil || !refundEvents[ev.Type] {
		return ProviderRefund{}, false
	}
	var ref stripe.Refund
	if err := json.Unmarshal(ev.Data.Raw, &ref); err != nil {
		return ProviderRefund{}, false
	}
	if !ValidStripeID(ref.ID) || ref.Amount <= 0 {
		return ProviderRefund{}, false
	}
	var intentRef, chargeRef string
	if ref.PaymentIntent != nil && ValidStripeID(ref.PaymentIntent.ID) {
		intentRef = ref.PaymentIntent.ID
	}
	if ref.Charge != nil && ValidStripeID(ref.Charge.ID) {
		chargeRef = ref.Charge.ID
	}
	return providerRefundFromStripe(&ref, intentRef, chargeRef, ev.Created)
}

// RefundsFromCharge reads refund objects embedded in a charge.refunded payload.
func RefundsFromCharge(ev *stripe.Event) ([]ProviderRefund, bool) {
	if ev == nil || ev.Data == nil || ev.Type != chargeRefundedEvent {
		return nil, false
	}
	var ch stripe.Charge
	if err := json.Unmarshal(ev.Data.Raw, &ch); err != nil {
		return nil, false
	}
	if !ValidStripeID(ch.ID) {
		return nil, false
	}
	var intentRef string
	if ch.PaymentIntent != nil && ValidStripeID(ch.PaymentIntent.ID) {
		intentRef = ch.PaymentIntent.ID
	}
	if ch.Refunds == nil || len(ch.Refunds.Data) == 0 {
		if intentRef == "" {
			return nil, false
		}
		return nil, true
	}
	out := make([]ProviderRefund, 0, len(ch.Refunds.Data))
	for i := range ch.Refunds.Data {
		if pr, ok := providerRefundFromStripe(ch.Refunds.Data[i], intentRef, ch.ID, ev.Created); ok {
			out = append(out, pr)
		}
	}
	return out, len(out) > 0 || intentRef != ""
}

func providerRefundFromStripe(ref *stripe.Refund, intentRef, chargeRef string, eventCreated int64) (ProviderRefund, bool) {
	if ref == nil || !ValidStripeID(ref.ID) || ref.Amount <= 0 || intentRef == "" {
		return ProviderRefund{}, false
	}
	status, ok := refundStatus(ref.Status)
	if !ok {
		return ProviderRefund{}, false
	}
	pr := ProviderRefund{
		ProviderRef:       ref.ID,
		PaymentIntentRef:  intentRef,
		ChargeRef:         chargeRef,
		AmountCents:       ref.Amount,
		Currency:          string(ref.Currency),
		Status:            status,
		ProviderUpdatedAt: eventCreated,
	}
	if ref.Metadata != nil {
		pr.RequestKey = ref.Metadata[refundRequestKeyTag]
	}
	if ref.FailureReason != "" {
		pr.FailureReason = string(ref.FailureReason)
	}
	return pr, true
}

// PaymentIntentFromCapture reads the PaymentIntent id from a paid session event.
func PaymentIntentFromCapture(ev *stripe.Event) (string, bool) {
	if ev == nil || ev.Data == nil || !captureEvents[ev.Type] {
		return "", false
	}
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(ev.Data.Raw, &sess); err != nil {
		return "", false
	}
	if sess.PaymentIntent == nil || !ValidStripeID(sess.PaymentIntent.ID) {
		return "", false
	}
	return sess.PaymentIntent.ID, true
}

// RefundReconcileBudget bounds the transaction that may read current Stripe state.
const RefundReconcileBudget = 2 * time.Second

const maxRefundLookups = 4

var errRefundConflict = errors.New("payment: refund observation could not be resolved")

// currentRefundStatus reads only provider status; attribution and amounts remain
// those of the authenticated event whose identity has already been checked.
func (g *Gateway) currentRefundStatus(ctx context.Context, r ProviderRefund) (ProviderRefund, error) {
	if g == nil || g.refundClient == nil {
		return ProviderRefund{}, ErrDisabled
	}
	current, err := g.refundClient.V1Refunds.Retrieve(ctx, r.ProviderRef, nil)
	if err != nil {
		return ProviderRefund{}, fmt.Errorf("retrieve conflicting refund: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ProviderRefund{}, err
	}
	if current == nil || current.ID != r.ProviderRef || current.PaymentIntent == nil ||
		current.PaymentIntent.ID != r.PaymentIntentRef || current.Amount != r.AmountCents ||
		!strings.EqualFold(string(current.Currency), r.Currency) ||
		(r.ChargeRef != "" && (current.Charge == nil || current.Charge.ID != r.ChargeRef)) {
		return ProviderRefund{}, errRefundConflict
	}
	status, ok := refundStatus(current.Status)
	if !ok {
		return ProviderRefund{}, errRefundConflict
	}
	r.Status = status
	r.FailureReason = string(current.FailureReason)
	return r, nil
}
