//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/telemetry"
)

func TestUnknownMethodsHaveBoundedRouterTelemetry(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousTrace := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previousTrace); _ = tp.Shutdown(t.Context()) })
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousMeter := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(previousMeter); _ = mp.Shutdown(t.Context()) })
	if _, err := telemetry.Setup(t.Context(), telemetry.Config{}); err != nil {
		t.Fatal(err)
	}
	storePool, err := openPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer storePool.Close()
	adminPool, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	handler := newRouter(&RouterConfig{Pool: storePool, AdminPool: adminPool}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	methods := []string{http.MethodGet, http.MethodPost}
	for i := range 24 {
		methods = append(methods, fmt.Sprintf("CUSTOM%03d", i))
	}
	for _, method := range methods {
		req := httptest.NewRequestWithContext(t.Context(), method, "/no-such-telemetry-route", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("%s unmatched response=%d", method, rec.Code)
		}
	}

	spanCounts := map[string]int{}
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			if attr.Key != "http.method" {
				continue
			}
			spanCounts[span.Name]++
			switch span.Name {
			case "GET unmatched", "POST unmatched", "_OTHER unmatched":
				wantMethod := span.Name[:len(span.Name)-len(" unmatched")]
				if attr.Value.AsString() != wantMethod {
					t.Errorf("span %q method=%q", span.Name, attr.Value.AsString())
				}
			default:
				t.Errorf("unbounded HTTP span name %q", span.Name)
			}
		}
	}
	if spanCounts["GET unmatched"] != 1 || spanCounts["POST unmatched"] != 1 || spanCounts["_OTHER unmatched"] != 24 || len(spanCounts) != 3 {
		t.Errorf("unmatched span names=%v", spanCounts)
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.WithoutCancel(t.Context()), &data); err != nil {
		t.Fatal(err)
	}
	routeCounts := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != "goen.http.server.requests" {
				continue
			}
			for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
				route, _ := point.Attributes.Value("http.route")
				routeCounts[route.AsString()] += point.Value
			}
		}
	}
	if routeCounts["GET unmatched"] != 1 || routeCounts["POST unmatched"] != 1 || routeCounts["_OTHER unmatched"] != 24 || len(routeCounts) != 3 {
		t.Errorf("unmatched metric routes=%v", routeCounts)
	}
}
