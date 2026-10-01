//go:build integration

package payment

import (
	"context"

	stripe "github.com/stripe/stripe-go/v86"
)

// NewGatewayAt is NewGateway against a Stripe stand-in at stripeURL, through
// the production HTTP client and with no network retries, so one refused
// request is one request.
func NewGatewayAt(apiKey, webhookSecret, baseURL, stripeURL string) (*Gateway, error) {
	noRetries := int64(0)
	return newGateway(apiKey, webhookSecret, baseURL, &stripe.BackendConfig{
		URL: stripe.String(stripeURL), MaxNetworkRetries: &noRetries,
	})
}

// WebhookEvent exposes a verified provider event only to integration fixtures.
type WebhookEvent = webhookEvent

// WebhookTx exposes the claimed-event transaction capability only to
// integration fixtures.
type WebhookTx = webhookTx

// ProcessWebhook exposes the handler-owned atomic claim/apply operation only
// in integration builds.
func (s *Store) ProcessWebhook(
	ctx context.Context,
	ev *WebhookEvent,
	apply func(context.Context, *WebhookTx) error,
) (bool, error) {
	return s.processWebhook(ctx, ev, apply)
}
