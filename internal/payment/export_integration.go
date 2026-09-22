//go:build integration

package payment

import (
	"context"
	"net/http"
)

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

// SetRefundTransport redirects the production refund client's HTTP boundary for
// integration fixtures, retaining its timeout and zero-retry SDK configuration.
func SetRefundTransport(g *Gateway, transport http.RoundTripper) {
	g.refundHTTP.Transport = transport
}
