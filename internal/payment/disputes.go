package payment

import (
	"context"
	"fmt"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

// disputeWindow is how far back a dispute is looked for. Stripe's response
// deadlines are a few weeks after the dispute opens, so one still waiting for an
// answer is far newer than this.
const disputeWindow = 120 * 24 * time.Hour

// Dispute is a card dispute Stripe is waiting for the shop to answer.
type Dispute struct {
	ID          string
	AmountCents int64
	// RespondBy is zero when the cardholder's bank allows no response.
	RespondBy time.Time
	// SessionID is the Checkout Session that took the disputed payment, empty
	// when Stripe names no payment intent or no session used it.
	SessionID string
}

// DisputesNeedingResponse reads, live from Stripe, the disputes whose status
// asks the shop to respond, each with the Checkout Session it was paid through.
func (g *Gateway) DisputesNeedingResponse(ctx context.Context) ([]Dispute, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	since := time.Now().Add(-disputeWindow).Unix()
	list := g.client.V1Disputes.List(ctx, &stripe.DisputeListParams{
		CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: since},
	})
	var out []Dispute
	for d, err := range list.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("list disputes: %w", err)
		}
		if d == nil || !ValidStripeID(d.ID) {
			return nil, fmt.Errorf("%w: a listed dispute has no valid id", errInvalidStripeResponse)
		}
		if d.Status != stripe.DisputeStatusNeedsResponse &&
			d.Status != stripe.DisputeStatusWarningNeedsResponse {
			continue
		}
		dispute := Dispute{ID: d.ID, AmountCents: d.Amount}
		if d.EvidenceDetails != nil && d.EvidenceDetails.DueBy > 0 {
			dispute.RespondBy = time.Unix(d.EvidenceDetails.DueBy, 0)
		}
		if d.PaymentIntent != nil && ValidStripeID(d.PaymentIntent.ID) {
			sessionID, err := g.sessionOf(ctx, d.PaymentIntent.ID)
			if err != nil {
				return nil, err
			}
			dispute.SessionID = sessionID
		}
		out = append(out, dispute)
	}
	return out, nil
}

func (g *Gateway) sessionOf(ctx context.Context, paymentIntentID string) (string, error) {
	params := &stripe.CheckoutSessionListParams{PaymentIntent: stripe.String(paymentIntentID)}
	params.Limit = stripe.Int64(1)
	for sess, err := range g.client.V1CheckoutSessions.List(ctx, params).All(ctx) {
		if err != nil {
			return "", fmt.Errorf("list checkout sessions of %s: %w", paymentIntentID, err)
		}
		if sess == nil || !ValidStripeID(sess.ID) {
			return "", fmt.Errorf("%w: a session of %s has no valid id", errInvalidStripeResponse, paymentIntentID)
		}
		return sess.ID, nil
	}
	return "", nil
}

// DisputeURL is where the dispute is answered: the Stripe Dashboard, in test
// mode when the key is one.
func (g *Gateway) DisputeURL(id string) string {
	if g.sandbox {
		return "https://dashboard.stripe.com/test/disputes/" + id
	}
	return "https://dashboard.stripe.com/disputes/" + id
}
