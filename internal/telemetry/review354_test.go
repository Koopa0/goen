package telemetry

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func outboxMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		_ = mp.Shutdown(context.WithoutCancel(t.Context()))
	})
	return reader
}

func exportedOutbox(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]int64)
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if !strings.HasPrefix(measurement.Name, "goen.outbox.") {
				continue
			}
			points := measurement.Data.(metricdata.Gauge[int64]).DataPoints
			if len(points) != 1 {
				t.Fatalf("%s has %d observations, want 1", measurement.Name, len(points))
			}
			values[measurement.Name] = points[0].Value
		}
	}
	return values
}

func awaitOutboxExport(t *testing.T, reader *sdkmetric.ManualReader, accepts func(map[string]int64) bool) map[string]int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		values := exportedOutbox(t, reader)
		if accepts(values) {
			return values
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox observations did not reach expected state: %v", values)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOutboxCollectorLifetimeAndUnknownState(t *testing.T) {
	// Each process startup owns its callback; a previous registration must not
	// prevent a new provider from receiving health observations.
	for i := range 2 {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			reader := outboxMetricReader(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{})
			release := make(chan struct{})
			RegisterOutboxCollector(ctx, time.Hour, func(readCtx context.Context) (OutboxStats, error) {
				close(started)
				select {
				case <-readCtx.Done():
					return OutboxStats{}, readCtx.Err()
				case <-release:
					return OutboxStats{Pending: 7, OldestSeconds: 13, Stuck: 2}, nil
				}
			})
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("first health read waits for the polling interval")
			}
			unknown := exportedOutbox(t, reader)
			if up, ok := unknown["goen.outbox.collector.up"]; !ok || up != 0 || len(unknown) != 1 {
				t.Fatalf("unknown queue was exported as data: %v", unknown)
			}
			close(release)
			values := awaitOutboxExport(t, reader, func(m map[string]int64) bool { return m["goen.outbox.collector.up"] == 1 })
			if values["goen.outbox.pending"] != 7 || values["goen.outbox.oldest_seconds"] != 13 || values["goen.outbox.stuck"] != 2 {
				t.Fatalf("export does not match the successful health read: %v", values)
			}
			cancel()
			if values = exportedOutbox(t, reader); len(values) != 0 {
				t.Fatalf("canceled collector still publishes health: %v", values)
			}
		})
	}
}

func TestOutboxCollectorFailureAndRecovery(t *testing.T) {
	reader := outboxMetricReader(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var fail atomic.Bool
	RegisterOutboxCollector(ctx, 20*time.Millisecond, func(context.Context) (OutboxStats, error) {
		if fail.Load() {
			return OutboxStats{}, errors.New("health database unavailable")
		}
		return OutboxStats{Pending: 4, Stuck: 1}, nil
	})
	awaitOutboxExport(t, reader, func(m map[string]int64) bool { return m["goen.outbox.pending"] == 4 })
	fail.Store(true)
	values := awaitOutboxExport(t, reader, func(m map[string]int64) bool {
		up, exists := m["goen.outbox.collector.up"]
		return exists && up == 0
	})
	if len(values) != 1 {
		t.Fatalf("failed read retained stale queue measurements: %v", values)
	}
	fail.Store(false)
	awaitOutboxExport(t, reader, func(m map[string]int64) bool {
		return m["goen.outbox.collector.up"] == 1 && m["goen.outbox.pending"] == 4 && m["goen.outbox.stuck"] == 1
	})
}

func TestOutboxCollectorDiscardsReadAfterBudget(t *testing.T) {
	reader := outboxMetricReader(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan struct{})
	var calls atomic.Int32
	RegisterOutboxCollector(ctx, 20*time.Millisecond, func(readCtx context.Context) (OutboxStats, error) {
		if calls.Add(1) == 2 {
			close(returned)
		}
		deadline, ok := readCtx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Millisecond {
			t.Error("outbox read has no bounded polling deadline")
		}
		<-readCtx.Done()
		return OutboxStats{Pending: 47}, nil
	})
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("outbox health read did not time out")
	}
	values := exportedOutbox(t, reader)
	if up, ok := values["goen.outbox.collector.up"]; !ok || up != 0 || len(values) != 1 {
		t.Fatalf("late success published misleading health data: %v", values)
	}
}

func TestReview354ConcurrentOutboxPollAndExport(t *testing.T) {
	reader := outboxMetricReader(t)
	var seq atomic.Int64
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	RegisterOutboxCollector(ctx, 10*time.Millisecond, func(context.Context) (OutboxStats, error) {
		n := seq.Add(1)
		return OutboxStats{Pending: n, OldestSeconds: n, Stuck: n}, nil
	})
	awaitOutboxExport(t, reader, func(m map[string]int64) bool { return m["goen.outbox.pending"] > 0 })
	for range 100 {
		values := exportedOutbox(t, reader)
		if values["goen.outbox.collector.up"] == 1 && (values["goen.outbox.pending"] != values["goen.outbox.oldest_seconds"] || values["goen.outbox.pending"] != values["goen.outbox.stuck"]) {
			t.Fatalf("queue fields came from different observations: %v", values)
		}
		time.Sleep(time.Millisecond)
	}
	if seq.Load() < 2 {
		t.Fatal("concurrent export did not include repeated health polls")
	}
}
