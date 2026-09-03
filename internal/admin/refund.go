package admin

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/payment"
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

// ErrRefundCreateRejected is a decision from Stripe's refund-CREATE endpoint
// that no refund object was created. It is deliberately narrower than a
// *stripe.Error: the preliminary LIST uses the same error type, and treating a
// failed lookup as a rejected refund would free captured money for a second
// claim while the first outcome is still unknown.
var ErrRefundCreateRejected = errors.New("admin: Stripe rejected refund creation")

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
	if !payment.ValidStripeID(sessionID) {
		return "", errors.New("read checkout session: invalid Stripe session id")
	}
	sess, err := s.client.V1CheckoutSessions.Retrieve(ctx, sessionID,
		&stripe.CheckoutSessionRetrieveParams{
			Expand: []*string{stripe.String("payment_intent")},
		})
	if err != nil {
		return "", fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess == nil || !payment.ValidStripeID(sess.ID) || sess.ID != sessionID {
		return "", fmt.Errorf("checkout session %s returned a different or invalid session id", sessionID)
	}
	if sess.PaymentIntent == nil || !payment.ValidStripeID(sess.PaymentIntent.ID) {
		return "", fmt.Errorf("checkout session %s has no valid payment intent", sessionID)
	}
	return sess.PaymentIntent.ID, nil
}

const refundKeyTag = "goen_request_key"

func (s StripeRefunder) refundFor(
	ctx context.Context, paymentIntentID, requestKey string, amountCents int64,
) (id string, state RefundState, found bool, err error) {
	list := s.client.V1Refunds.List(ctx, &stripe.RefundListParams{
		PaymentIntent: stripe.String(paymentIntentID),
	})
	var matchedID string
	var matchedState RefundState
	for ref, listErr := range list.All(ctx) {
		if listErr != nil {
			return "", "", false, fmt.Errorf(
				"list refunds for %s: %w", paymentIntentID, listErr)
		}
		if ref == nil || !payment.ValidStripeID(ref.ID) {
			return "", "", false, fmt.Errorf(
				"list refunds for %s returned an invalid refund id", paymentIntentID)
		}
		if ref.Metadata[refundKeyTag] != requestKey {
			continue
		}
		if factErr := refundMatchesClaim(ref, paymentIntentID, requestKey, amountCents); factErr != nil {
			return "", "", false, factErr
		}
		known, stateErr := refundState(ref.Status)
		if stateErr != nil {
			return "", "", false, fmt.Errorf(
				"refund %s for %s: %w", ref.ID, paymentIntentID, stateErr)
		}
		if matchedID != "" {
			return "", "", false, fmt.Errorf(
				"multiple refunds for %s carry request key %q", paymentIntentID, requestKey)
		}
		matchedID, matchedState = ref.ID, known
	}
	return matchedID, matchedState, matchedID != "", nil
}

func (s StripeRefunder) Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, RefundState, error) {
	if s.client == nil {
		return "", "", ErrNoRefunder
	}
	if !payment.ValidStripeID(paymentIntentID) {
		return "", "", errors.New("refund: invalid Stripe payment intent id")
	}
	if amountCents <= 0 {
		return "", "", errors.New("refund: amount must be positive")
	}
	// Asked BEFORE creating one: Stripe's idempotency key expires after 24 hours.
	if existing, state, found, err := s.refundFor(
		ctx, paymentIntentID, requestKey, amountCents,
	); err != nil {
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
		wrapped := fmt.Errorf("create refund for %s: %w", paymentIntentID, err)
		if rejectedRefundCreate(err) {
			return "", "", fmt.Errorf("%w: %w", ErrRefundCreateRejected, wrapped)
		}
		return "", "", wrapped
	}
	if factErr := refundMatchesClaim(ref, paymentIntentID, requestKey, amountCents); factErr != nil {
		return "", "", fmt.Errorf("create refund for %s returned mismatched facts: %w",
			paymentIntentID, factErr)
	}
	state, err := refundState(ref.Status)
	if err != nil {
		return "", "", fmt.Errorf("refund %s for %s: %w", ref.ID, paymentIntentID, err)
	}
	return ref.ID, state, nil
}

// refundMatchesClaim refuses a provider object that merely shares metadata but
// does not represent the exact local money claim. A mismatch is ambiguous, not
// a provider rejection: the durable pending row must stay claimed for recovery.
func refundMatchesClaim(
	ref *stripe.Refund, paymentIntentID, requestKey string, amountCents int64,
) error {
	if ref == nil || !payment.ValidStripeID(ref.ID) {
		return errors.New("stripe returned an invalid refund id")
	}
	if ref.Amount != amountCents {
		return fmt.Errorf("refund %s amount is %d, want %d", ref.ID, ref.Amount, amountCents)
	}
	if ref.Currency != stripe.Currency(payment.Currency) {
		return fmt.Errorf("refund %s currency is %q, want %q",
			ref.ID, ref.Currency, payment.Currency)
	}
	if ref.PaymentIntent == nil || !payment.ValidStripeID(ref.PaymentIntent.ID) ||
		ref.PaymentIntent.ID != paymentIntentID {
		return fmt.Errorf("refund %s payment intent is not %s", ref.ID, paymentIntentID)
	}
	if ref.Metadata[refundKeyTag] != requestKey {
		return fmt.Errorf("refund %s request key does not match the claim", ref.ID)
	}
	return nil
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

// rejectedRefundCreate is called only at the CREATE boundary. The same Stripe
// error kinds from the preceding LIST do not say whether a refund exists.
func rejectedRefundCreate(err error) bool {
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
