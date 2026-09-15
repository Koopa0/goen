package telemetry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// OutboxStats mirrors the outbox fields from WorkerHealth without importing admin.
type OutboxStats struct {
	Pending       int64
	OldestSeconds int64
	Stuck         int64
}

// OutboxReader returns current outbox backlog using the worker health predicates.
type OutboxReader func(ctx context.Context) (OutboxStats, error)

var (
	outboxPending            metric.Int64ObservableGauge
	outboxOldest             metric.Int64ObservableGauge
	outboxStuck              metric.Int64ObservableGauge
	outboxLatest             atomic.Pointer[outboxSnapshot]
	outboxCallbackMu         sync.Mutex
	outboxCallbackRegistered bool
)

func initOutboxInstruments(m metric.Meter) error {
	var err error
	outboxPending, err = m.Int64ObservableGauge(
		"goen.outbox.pending",
		metric.WithDescription("Undelivered outbox messages"),
	)
	if err != nil {
		return fmt.Errorf("outbox pending gauge: %w", err)
	}
	outboxOldest, err = m.Int64ObservableGauge(
		"goen.outbox.oldest_seconds",
		metric.WithDescription("Age in seconds of the oldest due undelivered message"),
	)
	if err != nil {
		return fmt.Errorf("outbox oldest gauge: %w", err)
	}
	outboxStuck, err = m.Int64ObservableGauge(
		"goen.outbox.stuck",
		metric.WithDescription("Undelivered outbox messages at or above max attempts"),
	)
	if err != nil {
		return fmt.Errorf("outbox stuck gauge: %w", err)
	}
	return nil
}

// RegisterOutboxCollector polls reader on interval and publishes outbox gauges.
func RegisterOutboxCollector(ctx context.Context, interval time.Duration, reader OutboxReader) {
	if reader == nil || interval <= 0 {
		return
	}
	m := otel.Meter("github.com/koopa0/goen/internal/telemetry")
	if outboxPending == nil {
		if err := initOutboxInstruments(m); err != nil {
			return
		}
	}
	outboxLatest.Store(&outboxSnapshot{})
	outboxCallbackMu.Lock()
	if !outboxCallbackRegistered {
		if _, err := m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			snap := outboxLatest.Load()
			o.ObserveInt64(outboxPending, snap.Pending)
			o.ObserveInt64(outboxOldest, snap.OldestSeconds)
			o.ObserveInt64(outboxStuck, snap.Stuck)
			return nil
		}, outboxPending, outboxOldest, outboxStuck); err == nil {
			outboxCallbackRegistered = true
		}
	}
	outboxCallbackMu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats, err := reader(ctx)
				if err == nil {
					outboxLatest.Store(&outboxSnapshot{
						Pending:       stats.Pending,
						OldestSeconds: stats.OldestSeconds,
						Stuck:         stats.Stuck,
					})
				}
			}
		}
	}()
}

type outboxSnapshot struct {
	Pending       int64
	OldestSeconds int64
	Stuck         int64
}
