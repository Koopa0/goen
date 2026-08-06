package admin

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v86"
)

// RefundState is what a provider says a refund IS, in the vocabulary
// refunds_status_known accepts.
//
// It is a type of goen's rather than a pass-through of Stripe's, and the reason
// is not tidiness: Stripe spells its terminal cancel "canceled" and this schema
// spells it "cancelled". Handing the provider's string to settle_refund would
// be refused by the CHECK at the very end of a refund — after the money moved.
// Translating it here is what makes that a compile-time-shaped decision rather
// than a runtime surprise.
type RefundState string

// The five states refunds_status_known allows. 'requires_action' appeared in NO
// Go file in this repository until this type: the schema could express a refund
// Stripe had accepted and not settled, and goen could not.
const (
	RefundPending        RefundState = "pending"
	RefundRequiresAction RefundState = "requires_action"
	RefundSucceeded      RefundState = "succeeded"
	RefundFailed         RefundState = "failed"
	RefundCancelled      RefundState = "cancelled"
)

// Refunder is the Stripe side of paying money back.
//
// It is defined HERE, by the consumer, rather than in internal/payment: the
// back office needs two calls out of a gateway that does a great deal more, and
// this is the subset it depends on. internal/payment returns its concrete
// *Gateway and knows nothing about this interface.
type Refunder interface {
	// PaymentIntentFor resolves a Checkout Session id to the PaymentIntent a
	// refund must be issued against. goen stores the session id, because that
	// is the identifier it holds at the moment it must write the payment row.
	PaymentIntentFor(ctx context.Context, sessionID string) (string, error)
	// Refund asks Stripe to pay money back, keyed on requestKey so a retry
	// cannot pay twice.
	//
	// It answers with the provider's reference AND with what the provider says
	// the refund IS. A Stripe refund is genuinely 'pending' or
	// 'requires_action' in real life, and this call used to DISCARD the status
	// and return only the id — so the caller wrote 'succeeded' with a
	// succeeded_at stamp for money that had not moved. That is the inverse of
	// this repository's mistake #16: unknown was recorded as a value, and here
	// the value asserted was the good one.
	Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, RefundState, error)
}

// ErrNoRefunder is a back office running without Stripe credentials. Returns
// can still be approved; the money is then a manual job, and the refund row
// records that it was asked for.
var ErrNoRefunder = errors.New("admin: stripe is not configured")

// StripeRefunder is the production Refunder. Exported and returned concrete,
// because a constructor that returns an interface hides which implementation
// the caller got — the interface exists for the CONSUMER to depend on, not for
// the producer to hand back.
type StripeRefunder struct {
	client *stripe.Client
}

// NewRefunder wraps a Stripe client. A blank key yields a refunder that
// refuses, which is what a development environment without Stripe should do —
// loudly, rather than by silently marking money returned that never moved.
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
			// The intent is a bare id on the session unless it is expanded, and
			// goen needs the id itself — so ask for the object and read it off.
			Params: stripe.Params{Expand: []*string{stripe.String("payment_intent")}},
		})
	if err != nil {
		return "", fmt.Errorf("read checkout session %s: %w", sessionID, err)
	}
	if sess.PaymentIntent == nil || sess.PaymentIntent.ID == "" {
		// An unpaid session has no intent. Refunding one would be refunding
		// money that never arrived.
		return "", fmt.Errorf("checkout session %s has no payment intent", sessionID)
	}
	return sess.PaymentIntent.ID, nil
}

// refundKeyTag is the metadata field carrying goen's own request key onto the
// Stripe refund object.
//
// Metadata rather than the idempotency key, because they have different
// lifetimes: an idempotency key is forgotten after 24 hours and metadata is part
// of the object for as long as it exists. This is what lets a retry days later
// still recognise its own refund.
const refundKeyTag = "goen_request_key"

// refundFor is the refund this request key already created, if there is one.
//
// Listing rather than a lookup, because goen has no Stripe refund id to look up
// — the case this exists for is precisely the one where goen never heard the
// answer. The page size is the default; a payment intent with more refunds than
// that has a reconciliation problem no automatic retry should be resolving.
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
	// Ask whether this refund already exists BEFORE creating one, and do not
	// rely on Stripe's idempotency key to answer it.
	//
	// The key is `return:<uuid>` and never changes, which is what makes a retry
	// safe — for 24 HOURS. Stripe drops idempotency keys after that, so a refund
	// left 'pending' by an ambiguous transport error (goen did not hear the
	// answer; Stripe did create it) and retried the next morning would be created
	// a SECOND time. goen cannot see the first: RefundedSoFar deliberately
	// excludes this return's own request_key, which is the change that made retry
	// possible at all, and settle_refund updates the one row — so
	// refunds_within_capture still sums to a single refund and the books say once
	// while the customer was paid twice.
	//
	// Only a PARTIAL refund can reach it. A full one is refused by Stripe as
	// already-refunded and correctly marked failed — and a partial refund is
	// precisely what splitRefund exists to produce.
	//
	// The metadata below is what makes the question answerable forever: it lives
	// on the refund object, not in a 24-hour window.
	if existing, state, found, err := s.refundFor(ctx, paymentIntentID, requestKey); err != nil {
		return "", "", err
	} else if found {
		return existing, state, nil
	}

	ref, err := s.client.V1Refunds.Create(ctx, &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntentID),
		Amount:        stripe.Int64(amountCents),
		Params: stripe.Params{
			// goen's own key, sent as Stripe's. The refund row carrying it is
			// committed BEFORE this call, so a crash between the request and
			// the response leaves something reconciliation can find — and a
			// retry under the same key is one refund at Stripe, not two.
			//
			// The SECOND line of defence, not the first: it expires, and the
			// lookup above does not.
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
// An UNRECOGNISED status is an ERROR rather than a default, and rather than the
// panic error-handling.md prescribes for a closed set. The set is not goen's
// and not closed: it belongs to a third party that can add to it, so a value
// this switch has never seen is a runtime condition and not a programmer who
// forgot a case. The caller then leaves the row as open_refund wrote it —
// 'pending' — which is the honest record of "asked, answer not understood", and
// /admin/health lists it for a person. Guessing 'succeeded' is what this whole
// change exists to stop; guessing 'failed' would free the capture's allowance
// to be spent a second time.
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
		// The one spelling that differs. See RefundState.
		return RefundCancelled, nil
	default:
		return "", fmt.Errorf("stripe refund status %q is not one goen records", status)
	}
}

// declinedByStripe reports whether Stripe made a DECISION, as opposed to goen
// never having heard one.
//
// Only a decision may be written as a terminal 'failed'. Every error used to
// be: a network timeout, a 500, a cancelled context — all of which are states
// in which Stripe may well HAVE refunded and goen simply did not hear the
// answer. Marking those failed frees the capture's allowance to be claimed
// again and tells reconciliation the money is still at the shop. Unknown is not
// failure, which is mistake #16's rule applied to a status instead of a last4.
//
// The question is asked of the SDK's typed error rather than of its message —
// error-handling.md forbids strings.Contains on an error, and a provider's
// wording is not an API. card_error and invalid_request_error are Stripe
// saying no (already refunded, charge not refundable, amount too large).
// api_error is Stripe's own fault, rate_limit is "ask again", and
// idempotency_error means a refund under this key may already exist — none of
// the three is an answer about the money.
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

// Compile-time proof that the production type still satisfies what the back
// office asks for. Without it a signature drift shows up only where the two are
// wired together, which is in main.
var _ Refunder = StripeRefunder{}
