package telemetry_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/telemetry"
)

func TestProviderErrorsExportOnlyTheOutcome(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = tp.Shutdown(t.Context()) })

	canaries := []string{"alice@example.com", "123 Private Street", "Bearer secret-payment-token", "cs_private_123"}
	ctx, call := telemetry.BeginProvider(t.Context(), telemetry.ProviderStripe, "checkout.create")
	call.End(ctx, errors.New(strings.Join(canaries, " ")))
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Description != "error" {
		t.Fatalf("provider failure lost its bounded outcome: %+v", spans)
	}
	encoded, err := json.Marshal(spans)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range canaries {
		if strings.Contains(string(encoded), value) {
			t.Errorf("exported provider error leaked %q", value)
		}
	}
}

func TestQueryTelemetryBoundsNamesAndExcludesSQLAndArguments(t *testing.T) {
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
	tracer := telemetry.QueryTracer{Role: telemetry.PoolStore}
	for i := range 50 {
		ctx := tracer.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{
			SQL:  fmt.Sprintf("-- name: private_request_%d :one\nSELECT 'secret-row-%d'", i, i),
			Args: []any{"alice@example.com", "Bearer private-token"},
		})
		tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("secret database detail alice@example.com")})
	}
	ctx := tracer.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "-- name: ProductImages :many\nSELECT private_image_url"})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	spans := exporter.GetSpans()
	if len(spans) != 51 || spans[50].Name != "postgres.product.images" {
		t.Fatalf("named query span missing: count=%d", len(spans))
	}
	for _, span := range spans[:50] {
		if span.Name != "postgres.other" {
			t.Errorf("unbounded query name: %s", span.Name)
		}
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	var measured uint64
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "goen.db.query.duration" {
				points := measurement.Data.(metricdata.Histogram[float64]).DataPoints
				if len(points) != 2 {
					t.Errorf("query metric series=%d, want 2", len(points))
				}
				for _, point := range points {
					measured += point.Count
				}
			}
		}
	}
	if measured != 51 {
		t.Errorf("measured queries=%d, want 51", measured)
	}
	encoded, err := json.Marshal([]any{spans, data})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"private_request", "secret-row", "alice@example.com", "private-token", "private_image_url", "secret database detail"} {
		if strings.Contains(string(encoded), canary) {
			t.Errorf("query telemetry leaked %q", canary)
		}
	}
}
