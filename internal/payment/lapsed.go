package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/db"
)

// LapsedSessionGrace is how long after an order's last stock hold ran out the
// sweeper waits for Stripe's own checkout.session.expired before asking Stripe.
// A delivered event cancels the payment first, and the session is never asked
// about.
const LapsedSessionGrace = 15 * time.Minute

// LapsedSessionInterval is how often the sweeper looks for such sessions.
const LapsedSessionInterval = 5 * time.Minute

// LapsedSessionBatch bounds one pass: each session is one or two Stripe
// requests.
const LapsedSessionBatch = 50

// lapsedSessionBudget bounds what one session may take, Stripe and database
// together, so a slow Stripe cannot stall the pass.
const lapsedSessionBudget = 15 * time.Second

// SessionSweeper settles, on Stripe's word, the checkouts whose ending webhook
// never arrived. Until a session is closed locally, the hold sweeper cannot
// cancel its lapsed order, so the store credit and coupon it spent stay spent.
// It records each answer through the doors the pay route uses for the same
// answers: expired is cancelled, complete awaits reconciliation, and anything
// else is asked again next pass.
type SessionSweeper struct {
	store   *Store
	gateway *Gateway
}

func NewSessionSweeper(s *Store, g *Gateway) *SessionSweeper {
	if s == nil || g == nil {
		panic("payment: NewSessionSweeper requires a store and a gateway")
	}
	return &SessionSweeper{store: s, gateway: g}
}

// SessionSweep is what one pass did with the sessions it found.
type SessionSweep struct {
	// Expired sessions are cancelled locally; the hold sweeper's next pass
	// cancels their orders and returns the credit and coupon.
	Expired int
	// Complete sessions may have taken money, so they await a person on
	// /admin/health instead.
	Complete int
	// Deferred sessions are asked again next pass.
	Deferred int
}

// Sweep settles each lapsed session separately, so one Stripe or database
// failure does not take the batch with it.
func (w *SessionSweeper) Sweep(ctx context.Context, log *slog.Logger) (SessionSweep, error) {
	var done SessionSweep
	sessions, err := w.store.q.LapsedCheckoutSessions(ctx, db.LapsedCheckoutSessionsParams{
		Grace: pgtype.Interval{Microseconds: LapsedSessionGrace.Microseconds(), Valid: true},
		Batch: LapsedSessionBatch,
	})
	if err != nil {
		return done, fmt.Errorf("read lapsed checkout sessions: %w", err)
	}
	for i := range sessions {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		s := &sessions[i]
		status, settleErr := w.settle(ctx, s)
		switch {
		case settleErr != nil:
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			done.Deferred++
			level := slog.LevelError
			if errors.Is(settleErr, errStripeUnanswered) {
				level = slog.LevelWarn
			}
			log.Log(ctx, level, "settle a lapsed checkout session",
				"order", s.OrderNumber, "session", s.ProviderRef, "error", settleErr)
		case status == stripe.CheckoutSessionStatusComplete:
			done.Complete++
			log.WarnContext(ctx, "a lapsed checkout session is complete at Stripe; it awaits reconciliation",
				"order", s.OrderNumber, "session", s.ProviderRef)
		default:
			done.Expired++
			// An open session is one goen expired itself.
			log.InfoContext(ctx, "closed a lapsed checkout session",
				"order", s.OrderNumber, "session", s.ProviderRef, "stripe_status", status)
		}
	}
	return done, nil
}

// errStripeUnanswered marks a session Stripe could not be asked about or did
// not answer usably, as against a local write that failed.
var errStripeUnanswered = errors.New("payment: Stripe did not settle the session")

// settle mirrors retireObsoleteSession: only Stripe's explicit states move the
// payment, and an open session past its order's hold is expired first.
func (w *SessionSweeper) settle(
	ctx context.Context, s *db.LapsedCheckoutSessionsRow,
) (stripe.CheckoutSessionStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, lapsedSessionBudget)
	defer cancel()

	_, status, err := w.gateway.ResumeSession(ctx, s.ProviderRef)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errStripeUnanswered, err)
	}
	switch status {
	case stripe.CheckoutSessionStatusOpen:
		if err := w.gateway.ExpireSession(ctx, s.ProviderRef); err != nil {
			return "", fmt.Errorf("%w: %w", errStripeUnanswered, err)
		}
		fallthrough
	case stripe.CheckoutSessionStatusExpired:
		return status, w.store.recordExpiredPayment(ctx, s.OrderNumber, s.ProviderRef, s.IntendedAmountCents)
	case stripe.CheckoutSessionStatusComplete:
		return status, w.store.recordCompletePayment(ctx, s.OrderNumber, s.ProviderRef, s.IntendedAmountCents)
	default:
		return "", fmt.Errorf("%w: session %s has status %q", errStripeUnanswered, s.ProviderRef, status)
	}
}

// SweepForever sweeps once at start, so a host redeployed more often than the
// interval still settles lapsed sessions, and then every interval. It blocks;
// the caller owns the goroutine.
func (w *SessionSweeper) SweepForever(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(LapsedSessionInterval)
	defer t.Stop()
	for {
		done, err := w.Sweep(ctx, log)
		if err != nil && ctx.Err() == nil {
			log.ErrorContext(ctx, "sweep lapsed checkout sessions", "error", err)
		}
		if done != (SessionSweep{}) {
			log.InfoContext(ctx, "settled lapsed checkout sessions",
				"expired", done.Expired, "complete", done.Complete, "deferred", done.Deferred)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
