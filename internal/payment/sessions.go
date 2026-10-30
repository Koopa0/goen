package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/stripe/stripe-go/v86"
)

// CloseSessions expires the checkouts a cancelled order left open at Stripe,
// post-commit and best effort — the stock and the credit are already back. A nil
// or disabled gateway is a deployment with no Stripe key, where no session was
// ever opened.
func CloseSessions(ctx context.Context, g *Gateway, log *slog.Logger, number string, sessions []string) {
	if !g.Enabled() {
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

// SessionExpirer closes the checkouts of an order its customer is cancelling,
// before the cancellation, so cancelling cannot leave a session someone can
// still pay.
type SessionExpirer struct {
	store   *Store
	gateway *Gateway
}

func NewSessionExpirer(s *Store, g *Gateway) *SessionExpirer {
	if s == nil || g == nil {
		panic("payment: NewSessionExpirer requires a store and a gateway")
	}
	return &SessionExpirer{store: s, gateway: g}
}

// ExpireOpen expires every Checkout Session the order has open at Stripe and,
// only when Stripe has confirmed all of them expired, records each as cancelled
// as checkout.session.expired would, so the cancellation that follows finds
// nothing payable and the webhook, when it comes, changes nothing. A session
// that is complete, that Stripe will not expire or cannot be asked about is
// ErrStillPayable and nothing is recorded: the customer may have paid. It
// survives a client disconnect but has one short budget, because a cancelled
// local write would leave goen calling a session payable after Stripe closed it.
func (e *SessionExpirer) ExpireOpen(ctx context.Context, number string) error {
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	open, err := e.store.q.OpenSessionsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read open checkout sessions of %s: %w", number, err)
	}
	for _, id := range open {
		if err := e.confirmExpired(ctx, id); err != nil {
			return err
		}
	}
	for _, id := range open {
		if err := e.store.cancelExpiredPayment(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// confirmExpired expires one session, or learns that Stripe already had. Stripe
// refuses to expire anything but an open session, and its refusal does not say
// whether the checkout ended unpaid or was paid, so only a fresh read decides.
func (e *SessionExpirer) confirmExpired(ctx context.Context, id string) error {
	expireErr := e.gateway.ExpireSession(ctx, id)
	if expireErr == nil {
		return nil
	}
	_, status, readErr := e.gateway.ResumeSession(ctx, id)
	if readErr != nil {
		return fmt.Errorf("%w: %w", ErrStillPayable, errors.Join(expireErr, readErr))
	}
	if status == stripe.CheckoutSessionStatusExpired {
		return nil
	}
	return fmt.Errorf("%w: session %s is %q: %w", ErrStillPayable, id, status, expireErr)
}
