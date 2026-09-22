package telemetry

import (
	"context"
	"fmt"
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

type outboxInstruments struct {
	pending metric.Int64ObservableGauge
	oldest  metric.Int64ObservableGauge
	stuck   metric.Int64ObservableGauge
	up      metric.Int64ObservableGauge
}

func newOutboxInstruments(m metric.Meter) (outboxInstruments, error) {
	var instruments outboxInstruments
	var err error
	instruments.pending, err = m.Int64ObservableGauge(
		"goen.outbox.pending",
		metric.WithDescription("Undelivered outbox messages"),
	)
	if err != nil {
		return instruments, fmt.Errorf("outbox pending gauge: %w", err)
	}
	instruments.oldest, err = m.Int64ObservableGauge(
		"goen.outbox.oldest_seconds",
		metric.WithDescription("Age in seconds of the oldest due undelivered message"),
	)
	if err != nil {
		return instruments, fmt.Errorf("outbox oldest gauge: %w", err)
	}
	instruments.stuck, err = m.Int64ObservableGauge(
		"goen.outbox.stuck",
		metric.WithDescription("Undelivered outbox messages at or above max attempts"),
	)
	if err != nil {
		return instruments, fmt.Errorf("outbox stuck gauge: %w", err)
	}
	instruments.up, err = m.Int64ObservableGauge(
		"goen.outbox.collector.up",
		metric.WithDescription("One when the latest outbox health read succeeded and remains fresh"),
	)
	if err != nil {
		return instruments, fmt.Errorf("outbox collector gauge: %w", err)
	}
	return instruments, nil
}

// RegisterOutboxCollector polls immediately and on interval until ctx ends.
// Unknown, failed or stale reads must not look like a healthy empty queue.
func RegisterOutboxCollector(ctx context.Context, interval time.Duration, reader OutboxReader) {
	if reader == nil || interval <= 0 {
		return
	}
	m := otel.Meter("github.com/koopa0/goen/internal/telemetry")
	instruments, err := newOutboxInstruments(m)
	if err != nil {
		return
	}
	budget := min(interval, 5*time.Second)
	var latest atomic.Pointer[outboxSnapshot]
	registration, err := m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if ctx.Err() != nil {
			return nil
		}
		snap := latest.Load()
		if snap == nil || time.Since(snap.observedAt) > interval+budget {
			o.ObserveInt64(instruments.up, 0)
			return nil
		}
		o.ObserveInt64(instruments.up, 1)
		o.ObserveInt64(instruments.pending, snap.Pending)
		o.ObserveInt64(instruments.oldest, snap.OldestSeconds)
		o.ObserveInt64(instruments.stuck, snap.Stuck)
		return nil
	}, instruments.pending, instruments.oldest, instruments.stuck, instruments.up)
	if err != nil {
		return
	}

	go func() {
		defer func() { _ = registration.Unregister() }()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			readCtx, cancel := context.WithTimeout(ctx, budget)
			stats, readErr := reader(readCtx)
			if readErr == nil && readCtx.Err() == nil {
				latest.Store(&outboxSnapshot{OutboxStats: stats, observedAt: time.Now()})
			} else {
				latest.Store(nil)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

type outboxSnapshot struct {
	OutboxStats
	observedAt time.Time
}
