//go:build integration

package payment

import "context"

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
