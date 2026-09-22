//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/telemetry"
)

func TestProductQueryWaitSharesHTTPTraceAndBoundedMetrics(t *testing.T) {
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
	var logs bytes.Buffer
	logger := slog.New(telemetry.CorrelatedHandler(slog.NewJSONHandler(&logs, nil)))
	handler := newRouter(&RouterConfig{Pool: storePool, AdminPool: adminPool, BaseURL: "http://127.0.0.1"}, logger)
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		completed <- struct{}{}
	}))
	defer server.Close()
	var slug string
	if err = pool.QueryRow(t.Context(), `SELECT slug FROM products WHERE status='active' ORDER BY slug LIMIT 1`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err = lock.Exec(t.Context(), `LOCK TABLE product_images IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type response struct {
		id     string
		status int
		err    error
	}
	result := make(chan response, 1)
	requestCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() {
		req, reqErr := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/p/"+slug+"?token=private-query-canary", http.NoBody)
		if reqErr != nil {
			result <- response{err: reqErr}
			return
		}
		resp, callErr := server.Client().Do(req)
		if callErr != nil {
			result <- response{err: callErr}
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		result <- response{id: resp.Header.Get("X-Request-ID"), status: resp.StatusCode}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '-- name: ProductImages%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production ProductImages query never reached the held table")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if err = lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.status != http.StatusOK {
		t.Fatalf("slow query response=%+v", got)
	}
	<-completed
	spans := exporter.GetSpans()
	var request, query tracetest.SpanStub
	for _, span := range spans {
		switch span.Name {
		case "GET /p/{slug}":
			request = span
		case "postgres.product.images":
			query = span
		}
	}
	if !request.SpanContext.IsValid() || !query.SpanContext.IsValid() || query.Parent.SpanID() != request.SpanContext.SpanID() || query.SpanContext.TraceID() != request.SpanContext.TraceID() {
		t.Fatal("slow SQL did not export a child of the product HTTP span")
	}
	if query.EndTime.Sub(query.StartTime) < 100*time.Millisecond {
		t.Error("query span excludes the held-table execution wait")
	}
	if !bytes.Contains(logs.Bytes(), []byte(got.id)) || !bytes.Contains(logs.Bytes(), []byte(request.SpanContext.TraceID().String())) {
		t.Error("request log does not identify the exported slow request")
	}
	for i := range 20 {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("%s/p/no-such-private-slug-%d", server.URL, i), http.NoBody)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		resp, callErr := server.Client().Do(req)
		if callErr != nil {
			t.Fatal(callErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		<-completed
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("missing product status=%d", resp.StatusCode)
		}
	}
	var metrics metricdata.ResourceMetrics
	if err = reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	var images uint64
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch measurement.Name {
			case "goen.http.server.requests":
				for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
					route, _ := point.Attributes.Value("http.route")
					status, _ := point.Attributes.Value("http.status_class")
					if route.AsString() != "GET /p/{slug}" {
						t.Errorf("unexpected route dimension=%s", route.AsString())
					}
					counts[status.AsString()] += point.Value
				}
			case "goen.db.query.duration":
				for _, point := range measurement.Data.(metricdata.Histogram[float64]).DataPoints {
					operation, _ := point.Attributes.Value("db.operation")
					if operation.AsString() == "product.images" {
						images += point.Count
					}
				}
			}
		}
	}
	if counts["2xx"] != 1 || counts["4xx"] != 20 || images != 1 {
		t.Errorf("request counts=%v image queries=%d, want 1/20/1", counts, images)
	}
	encoded, err := json.Marshal([]any{exporter.GetSpans(), metrics})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-query-canary", "no-such-private-slug"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("telemetry exported request data %q", secret)
		}
	}
}
