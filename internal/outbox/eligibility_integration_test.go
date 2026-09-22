//go:build integration

package outbox_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/outbox"
)

type eligibilityTraceKey struct{}
type eligibilityTrace struct {
	t               *testing.T
	delay           bool
	claims, windows atomic.Int32
}

func (p *eligibilityTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	var limit time.Duration
	switch {
	case strings.Contains(data.SQL, "-- name: ClaimOutbox"):
		p.claims.Add(1)
		limit = outbox.Lease - outbox.LeaseMargin
	case strings.Contains(data.SQL, "-- name: OutboxDeliveryWindow"):
		p.windows.Add(1)
		limit = outbox.EligibilityBudget
	default:
		return ctx
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > limit || time.Until(deadline) <= 0 {
		p.t.Errorf("query deadline=%v present=%t; want within %v", deadline, ok, limit)
	}
	return context.WithValue(ctx, eligibilityTraceKey{}, strings.Contains(data.SQL, "-- name: OutboxDeliveryWindow"))
}

func (p *eligibilityTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	isWindow, _ := ctx.Value(eligibilityTraceKey{}).(bool)
	if isWindow && p.delay {
		// Return a successful result late even when the transport ignores cancellation.
		time.Sleep(outbox.EligibilityBudget + 100*time.Millisecond)
	}
}

type eligibilitySender struct{ calls atomic.Int32 }

func (s *eligibilitySender) Send(context.Context, *email.Message) error { s.calls.Add(1); return nil }

func TestDeliveryEligibilityCannotSpendTheHandlersLease(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		name := "fast"
		if delayed {
			name = "late successful query"
		}
		t.Run(name, func(t *testing.T) {
			emptyOutbox(t)
			key := enqueueReceipt(t)
			tracer := &eligibilityTrace{t: t, delay: delayed}
			cfg := pool.Config().Copy()
			cfg.ConnConfig.Tracer = tracer
			p, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			sender := &eligibilitySender{}
			notifier := email.New(sender, "https://goen.test", "", "")
			worker := outbox.NewStore(p, quiet())
			worker.HandleJSON[email.OrderPaid](outbox.TopicOrderPaid, notifier.SendOrderPaid)
			delivered, failed, err := worker.Drain(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantDelivered, wantFailed := 1, 0
			if delayed {
				wantDelivered, wantFailed = 0, 1
			}
			if delivered != wantDelivered || failed != wantFailed || sender.calls.Load() != int32(wantDelivered) {
				t.Errorf("delivered=%d failed=%d sends=%d; want %d/%d/%d", delivered, failed, sender.calls.Load(), wantDelivered, wantFailed, wantDelivered)
			}
			if tracer.claims.Load() != 1 || tracer.windows.Load() != 1 {
				t.Fatalf("claim/window calls=%d/%d", tracer.claims.Load(), tracer.windows.Load())
			}
			if delayed {
				var attempts int
				var untouched bool
				if err := pool.QueryRow(t.Context(), `SELECT attempts, delivered_at IS NULL AND dropped_at IS NULL AND blocked_at IS NULL AND payload <> '{}'::jsonb AND available_at > clock_timestamp() FROM outbox_messages WHERE dedupe_key=$1`, key).Scan(&attempts, &untouched); err != nil {
					t.Fatal(err)
				}
				if attempts != 1 || !untouched {
					t.Errorf("expired eligibility changed original leased row: attempts=%d untouched=%t", attempts, untouched)
				}
			}
		})
	}
}
