package admin

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v86"
)

// RefundState is what a provider says a refund IS, as refunds_status_known spells it.
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
	PaymentIntentFor(ctx context.Context, sessionID string) (string, error)
	// Refund is keyed on requestKey so a retry cannot pay twice, and answers what
	// the provider says the refund IS: a real one is often 'pending'.
	Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, RefundState, error)
}

// ErrNoRefunder is a back office running without Stripe credentials.
var ErrNoRefunder = errors.New("admin: stripe is not configured")

// StripeRefunder is the production Refunder.
type StripeRefunder struct {
	client *stripe.Client
}

// NewRefunder wraps a Stripe client; a blank key yields one that refuses.
func NewRefunder(apiKey string) StripeRefunder {
	if apiKey == "" {
		return StripeRefunder{}
	}
	return StripeRefunder{client: stripe.NewClient(apiKey)}
}

func (s StripeRefunder) PaymentIntentFor(ctx context.Context, sessionID string) (string, error) {
	if s.client == nil {
		return "", ErrNoRefunder
	}
	sess, err := s.client.V1CheckoutSessions.Retrieve(ctx, sessionID,
		&stripe.CheckoutSessionRetrieveParams{
			Expand: []*string{stripe.String("payment_intent")},
		})
	if err != nil {
		return "", fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess.PaymentIntent == nil || sess.PaymentIntent.ID == "" {
		return "", fmt.Errorf("checkout session %s has no payment intent", sessionID)
	}
	return sess.PaymentIntent.ID, nil
}

const refundKeyTag = "goen_request_key"

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
	// Asked BEFORE creating one: Stripe's idempotency key expires after 24 hours.
	if existing, state, found, err := s.refundFor(ctx, paymentIntentID, requestKey); err != nil {
		return "", "", err
	} else if found {
		return existing, state, nil
	}

	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntentID),
		Amount:        new(amountCents),
		Metadata:      map[string]string{refundKeyTag: requestKey},
	}
	params.SetIdempotencyKey(requestKey)
	ref, err := s.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return "", "", fmt.Errorf("create refund for %s: %w", paymentIntentID, err)
	}
	state, err := refundState(ref.Status)
	if err != nil {
		return "", "", fmt.Errorf("refund %s for %s: %w", ref.ID, paymentIntentID, err)
	}
	return ref.ID, state, nil
}

// refundState maps Stripe's status; an unknown one errors rather than panics,
// because the set belongs to a third party.
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
		// Stripe spells it "canceled" and this schema spells it "cancelled".
		return RefundCancelled, nil
	default:
		return "", fmt.Errorf("stripe refund status %q is not one goen records", status)
	}
}

// declinedByStripe separates a DECISION from never having heard one: marking a
// timeout 'failed' frees the capture's allowance to be claimed twice.
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
