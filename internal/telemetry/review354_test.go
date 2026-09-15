package telemetry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkmetricdata "go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
)

func resetOutboxCollectorForTest() {
	outboxCallbackMu.Lock()
	outboxPending = nil
	outboxOldest = nil
	outboxStuck = nil
	outboxCallbackRegistered = false
	outboxLatest.Store(&outboxSnapshot{})
	outboxCallbackMu.Unlock()
}

func TestReview354ConcurrentOutboxPollAndExport(t *testing.T) {
	resetOutboxCollectorForTest()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })
	if _, err := Setup(t.Context(), Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	var seq atomic.Int64
	outboxReader := func(context.Context) (OutboxStats, error) {
		n := seq.Add(1)
		return OutboxStats{Pending: n, OldestSeconds: n, Stuck: n}, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	RegisterOutboxCollector(ctx, time.Millisecond, outboxReader)

	deadline := time.Now().Add(time.Second)
	var sawPending bool
	for time.Now().Before(deadline) {
		var data sdkmetricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &data); err != nil {
			t.Fatal(err)
		}
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "goen.outbox.pending" {
					continue
				}
				gauge, ok := metric.Data.(sdkmetricdata.Gauge[int64])
				if !ok || len(gauge.DataPoints) == 0 {
					continue
				}
				sawPending = true
				metricdatatest.AssertAggregationsEqual(t, gauge, metric.Data)
			}
		}
		time.Sleep(time.Millisecond)
	}
	if !sawPending {
		t.Fatal("expected outbox pending measurements during concurrent poll and export")
	}
}
