package refunds

import (
	"context"

	"github.com/koopa0/goen/internal/refund"
)

type Refunder interface {
	PaymentIntentFor(ctx context.Context, sessionID string) (string, error)
	// Refund is keyed on requestKey so a retry cannot pay twice, and answers what
	// the provider says the refund IS: a real one is often 'pending'.
	Refund(ctx context.Context, paymentIntentID, requestKey string, amountCents int64) (string, refund.Status, error)
}
