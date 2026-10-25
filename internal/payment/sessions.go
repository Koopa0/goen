package payment

import (
	"context"
	"log/slog"
	"time"
)

// CloseSessions expires the checkouts a cancelled order left open at Stripe,
// post-commit and best effort — the stock and the credit are already back. A nil
// or disabled gateway is a deployment with no Stripe key, where no session was
// ever opened.
func CloseSessions(ctx context.Context, g *Gateway, log *slog.Logger, number string, sessions []string) {
	if g == nil || !g.Enabled() {
		return
	}

	// The database cancellation has already committed. Keep request values for
	// tracing, but do not let a client disconnect turn the provider cleanup into
	// a no-op. One short budget bounds the whole best-effort batch.
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	for _, id := range sessions {
		if err := g.ExpireSession(ctx, id); err != nil {
			// Warn, not Error: Stripe refuses to expire anything but an OPEN
			// session, so a checkout completed a moment ago lands here.
			log.WarnContext(ctx, "expire checkout session of a cancelled order",
				"order_number", number, "session_id", id, "error", err)
		}
	}
}
