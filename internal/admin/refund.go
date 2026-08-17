package admin

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v86"
)

// RefundState is what a provider says a refund IS, in the vocabulary
// refunds_status_known accepts. Stripe spells its terminal cancel "canceled" and
// this schema spells it "cancelled", so the two are never interchangeable.
type RefundState string

// The five states refunds_status_known allows.
const (
	RefundPending        RefundState = "pending"
	RefundRequiresAction RefundState = "requires_action"
	RefundSucceeded      RefundState = "succeeded"
	RefundFailed         RefundState = "failed"
	RefundCancelled      RefundState = "cancelled"
)

// Refunder is the Stripe side of paying money back.
type Refunder interface {
	// PaymentIntentFor resolves a Checkout Session id to the PaymentIntent a
	// refund must be issued against; goen stores the session id.
	PaymentIntentFor(ctx context.Context, sessionID string) (string, error)
	// Refund asks Stripe to pay money back, keyed on requestKey so a retry
	// cannot pay twice, and answers with what the provider says the refund IS —
	// a real refund is often 'pending', and recording that as succeeded stamps
	// succeeded_at over money that has not moved.
	Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, RefundState, error)
}

// ErrNoRefunder is a back office running without Stripe credentials.
var ErrNoRefunder = errors.New("admin: stripe is not configured")

// StripeRefunder is the production Refunder.
type StripeRefunder struct {
	client *stripe.Client
}

// NewRefunder wraps a Stripe client. A blank key yields a refunder that refuses
// rather than one that silently marks money returned.
func NewRefunder(secretKey string) StripeRefunder {
	if secretKey == "" {
		return StripeRefunder{}
	}
	return StripeRefunder{client: stripe.NewClient(secretKey)}
}

func (s StripeRefunder) PaymentIntentFor(ctx context.Context, sessionID string) (string, error) {
	if s.client == nil {
		return "", ErrNoRefunder
	}
	sess, err := s.client.V1CheckoutSessions.Retrieve(ctx, sessionID,
		&stripe.CheckoutSessionRetrieveParams{
			Params: stripe.Params{Expand: []*string{stripe.String("payment_intent")}},
		})
	if err != nil {
		return "", fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess.PaymentIntent == nil || sess.PaymentIntent.ID == "" {
		// An unpaid session has no intent: money that never arrived.
		return "", fmt.Errorf("checkout session %s has no payment intent", sessionID)
	}
	return sess.PaymentIntent.ID, nil
}

// refundKeyTag carries goen's own request key onto the Stripe refund object.
// Metadata rather than the idempotency key: Stripe forgets an idempotency key
// after 24 hours, and metadata lives as long as the object.
const refundKeyTag = "goen_request_key"

// refundFor is the refund this request key already created, if there is one.
// Listed rather than looked up, because goen has no Stripe refund id to look up.
func (s StripeRefunder) refundFor(ctx context.Context, paymentIntentID, requestKey string,
) (id string, state RefundState, found bool, err error) {
	list := s.client.V1Refunds.List(ctx, &stripe.RefundListParams{
		PaymentIntent: stripe.String(paymentIntentID),
	})
	for ref, listErr := range list.All(ctx) {
		if listErr != nil {
			return "", "", false, fmt.Errorf(
				"list refunds for %s: %w", paymentIntentID, listErr)
		}
		if ref.Metadata[refundKeyTag] != requestKey {
			continue
		}
		known, stateErr := refundState(ref.Status)
		if stateErr != nil {
			return "", "", false, fmt.Errorf(
				"refund %s for %s: %w", ref.ID, paymentIntentID, stateErr)
		}
		return ref.ID, known, true, nil
	}
	return "", "", false, nil
}

func (s StripeRefunder) Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, RefundState, error) {
	if s.client == nil {
		return "", "", ErrNoRefunder
	}
	// Asked BEFORE creating one, because Stripe's idempotency key expires after
	// 24 hours: a refund retried the next morning would otherwise be created a
	// second time, and nothing here or in the books would show the duplicate.
	if existing, state, found, err := s.refundFor(ctx, paymentIntentID, requestKey); err != nil {
		return "", "", err
	} else if found {
		return existing, state, nil
	}

	ref, err := s.client.V1Refunds.Create(ctx, &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntentID),
		Amount:        stripe.Int64(amountCents),
		Params: stripe.Params{
			// The second line of defence, not the first: this expires and the
			// metadata lookup above does not.
			IdempotencyKey: stripe.String(requestKey),
			Metadata:       map[string]string{refundKeyTag: requestKey},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("create refund for %s: %w", paymentIntentID, err)
	}
	state, err := refundState(ref.Status)
	if err != nil {
		return "", "", fmt.Errorf("refund %s for %s: %w", ref.ID, paymentIntentID, err)
	}
	return ref.ID, state, nil
}

// refundState translates what Stripe said into what goen records.
//
// An unrecognised status is an ERROR, not the panic error-handling.md prescribes
// for a closed set: this set belongs to a third party that can add to it.
func refundState(status stripe.RefundStatus) (RefundState, error) {
	switch status {
	case stripe.RefundStatusSucceeded:
		return RefundSucceeded, nil
	case stripe.RefundStatusPending:
		return RefundPending, nil
	case stripe.RefundStatusRequiresAction:
		return RefundRequiresAction, nil
	case stripe.RefundStatusFailed:
		return RefundFailed, nil
	case stripe.RefundStatusCanceled:
		// The one spelling that differs.
		return RefundCancelled, nil
	default:
		return "", fmt.Errorf("stripe refund status %q is not one goen records", status)
	}
}

// declinedByStripe reports whether Stripe made a DECISION, as opposed to goen
// never having heard one. Only a decision may be written as a terminal 'failed':
// marking a timeout failed frees the capture's allowance to be claimed twice.
func declinedByStripe(err error) bool {
	e, ok := errors.AsType[*stripe.Error](err)
	if !ok {
		return false
	}
	switch e.Type {
	case stripe.ErrorTypeCard, stripe.ErrorTypeInvalidRequest:
		return true
	default:
		return false
	}
}

var _ Refunder = StripeRefunder{}
