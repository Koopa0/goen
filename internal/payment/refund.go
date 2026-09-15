package payment

import (
	"encoding/json"

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

// ProviderRefund is a verified refund object from a webhook payload.
type ProviderRefund struct {
	ProviderRef       string
	PaymentIntentRef  string
	ChargeRef         string
	AmountCents       int64
	Currency          string
	Status            string
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
	out := ProviderRefund{
		ProviderRef:       ref.ID,
		AmountCents:       ref.Amount,
		Currency:          string(ref.Currency),
		Status:            string(ref.Status),
		ProviderUpdatedAt: ref.Created,
	}
	if ref.PaymentIntent != nil && ValidStripeID(ref.PaymentIntent.ID) {
		out.PaymentIntentRef = ref.PaymentIntent.ID
	}
	if ref.Charge != nil && ValidStripeID(ref.Charge.ID) {
		out.ChargeRef = ref.Charge.ID
	}
	if ref.Metadata != nil {
		out.RequestKey = ref.Metadata[refundRequestKeyTag]
	}
	if ref.FailureReason != "" {
		out.FailureReason = string(ref.FailureReason)
	}
	if out.PaymentIntentRef == "" {
		return ProviderRefund{}, false
	}
	return out, true
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
		if pr, ok := providerRefundFromStripe(ch.Refunds.Data[i], intentRef, ch.ID); ok {
			out = append(out, pr)
		}
	}
	return out, len(out) > 0 || intentRef != ""
}

func providerRefundFromStripe(ref *stripe.Refund, intentRef, chargeRef string) (ProviderRefund, bool) {
	if ref == nil || !ValidStripeID(ref.ID) || ref.Amount <= 0 || intentRef == "" {
		return ProviderRefund{}, false
	}
	pr := ProviderRefund{
		ProviderRef:       ref.ID,
		PaymentIntentRef:  intentRef,
		ChargeRef:         chargeRef,
		AmountCents:       ref.Amount,
		Currency:          string(ref.Currency),
		Status:            string(ref.Status),
		ProviderUpdatedAt: ref.Created,
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
