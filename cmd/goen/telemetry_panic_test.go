package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/telemetry"
)

func TestRecoveredPanicSharesTheRequestSpanAndCompletionRecord(t *testing.T) {
	previous := otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(t.Context())
	})
	var logs bytes.Buffer
	logger := slog.New(telemetry.CorrelatedHandler(slog.NewJSONHandler(&logs, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic/{slug}", func(http.ResponseWriter, *http.Request) { panic("fixture") })
	capture := telemetry.CaptureHTTPRoute(mux)
	chrome := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.ServeHTTP(w, r.WithContext(r.Context()))
	})
	w := httptest.NewRecorder()
	withRequestTracing(chrome, logger).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/panic/private-slug", http.NoBody))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", w.Code)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("panic spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /panic/{slug}" {
		t.Errorf("panic route = %q, want bounded route template", span.Name)
	}
	seen := map[string]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var record struct {
			Msg       string `json:"msg"`
			RequestID string `json:"request_id"`
			TraceID   string `json:"trace_id"`
			SpanID    string `json:"span_id"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		seen[record.Msg]++
		if record.RequestID != w.Header().Get("X-Request-ID") || record.TraceID != span.SpanContext.TraceID().String() || record.SpanID != span.SpanContext.SpanID().String() {
			t.Errorf("%s does not identify the exported request span: %+v", record.Msg, record)
		}
	}
	if seen["request"] != 1 || seen["panic serving request"] != 1 {
		t.Errorf("panic log counts = %v, want one error and one request completion", seen)
	}
}
