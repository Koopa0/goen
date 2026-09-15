package telemetry_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkmetricdata "go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/telemetry"
)

func TestRouteLabelUsesPatternNotPath(t *testing.T) {
	t.Parallel()
	got := telemetry.RouteLabel(http.MethodGet, "GET /p/{slug}")
	want := "GET /p/{slug}"
	if got != want {
		t.Fatalf("RouteLabel() = %q, want %q", got, want)
	}
}

func TestSanitizeValueRedactsSensitiveData(t *testing.T) {
	t.Parallel()
	tests := []string{
		"Bearer sekret",
		"user@example.com",
		"550e8400-e29b-41d4-a716-446655440000",
		"token=abc123",
	}
	for _, in := range tests {
		if got := telemetry.SanitizeValue(in); got != "[redacted]" {
			t.Fatalf("SanitizeValue(%q) = %q, want [redacted]", in, got)
		}
	}
}

func TestProviderCallRecordsSpanAndMetric(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	if _, err := telemetry.Setup(t.Context(), telemetry.Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	ctx, call := telemetry.BeginProvider(t.Context(), telemetry.ProviderStripe, "checkout.create")
	call.End(ctx, nil)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "stripe.checkout.create" {
		t.Fatalf("span name = %q, want stripe.checkout.create", spans[0].Name)
	}
	for _, attr := range spans[0].Attributes {
		if strings.Contains(attr.Value.AsString(), "@") {
			t.Fatalf("span attribute leaked address: %v", attr)
		}
	}
}

func TestCacheEventUsesBoundedLabels(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { _ = mp.Shutdown(t.Context()) })

	if _, err := telemetry.Setup(t.Context(), telemetry.Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	telemetry.RecordCacheEvent(t.Context(), telemetry.CacheDomainProduct, telemetry.CacheMiss)
	telemetry.RecordCacheEvent(t.Context(), telemetry.CacheDomainProduct, telemetry.CacheHit)

	var data sdkmetricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.ScopeMetrics) == 0 {
		t.Fatal("expected cache metrics, got none")
	}
}

func TestCorrelatedHandlerAddsTraceFields(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	var buf bytes.Buffer
	log := slog.New(telemetry.CorrelatedHandler(slog.NewTextHandler(&buf, nil)))

	tracer := otel.Tracer("test")
	ctx, span := tracer.Start(t.Context(), "test-span")
	log.InfoContext(ctx, "hello")
	span.End()

	out := buf.String()
	if !strings.Contains(out, "trace_id=") {
		t.Fatalf("log missing trace_id: %q", out)
	}
	if strings.Contains(out, "@") {
		t.Fatalf("log leaked sensitive data: %q", out)
	}
}

func TestHTTPMiddlewareUsesRoutePatternThroughContextCopies(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{slug}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	inner := telemetry.CaptureHTTPRoute(mux)
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey("probe"), true)))
	})
	handler := telemetry.HTTP(outer)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/alpha", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "GET /p/{slug}" {
		t.Fatalf("span name = %q, want route template label", spans[0].Name)
	}
}

type contextKey string

func TestHTTPMiddlewareUsesRoutePattern(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{slug}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := telemetry.HTTP(mux)

	for _, slug := range []string{"alpha", "beta-gamma", "z" + strings.Repeat("9", 40)} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/"+slug, http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("span count = %d, want 3", len(spans))
	}
	for _, span := range spans {
		if span.Name != "GET /p/{slug}" {
			t.Fatalf("span name = %q, want route template label", span.Name)
		}
	}
}

func TestSetupShutdownWithoutExporterDoesNotBlock(t *testing.T) {
	shutdown, err := telemetry.Setup(t.Context(), telemetry.Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProviderOutcomeClassification(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	ctx2, call := telemetry.BeginProvider(ctx, telemetry.ProviderSMTP, "send")
	call.End(ctx2, context.DeadlineExceeded)
}

func TestDiagnosticsHandlerServesProfiles(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/debug/pprof/", http.NoBody)
	rec := httptest.NewRecorder()
	telemetry.DiagnosticsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "goroutine") {
		t.Fatalf("pprof index missing goroutine link: %q", body)
	}
}

func TestOutboxStatsShape(t *testing.T) {
	t.Parallel()
	stats := telemetry.OutboxStats{Pending: 1, OldestSeconds: 2, Stuck: 3}
	if diff := cmp.Diff(telemetry.OutboxStats{Pending: 1, OldestSeconds: 2, Stuck: 3}, stats); diff != "" {
		t.Fatalf("unexpected diff (-want +got):\n%s", diff)
	}
}
