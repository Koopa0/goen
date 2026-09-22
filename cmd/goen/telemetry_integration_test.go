//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/telemetry"
)

func TestHeldPoolExportsSaturationMetrics(t *testing.T) {
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

	p, err := openPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ap, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Close()

	telemetry.RegisterPool(telemetry.PoolStore, p)

	held := make([]*pgxpool.Conn, 0, int(p.Config().MaxConns))
	for range int(p.Config().MaxConns) {
		c, acquireErr := p.Acquire(t.Context())
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		held = append(held, c)
	}
	defer func() {
		for _, c := range held {
			c.Release()
		}
	}()

	stat := p.Stat()
	if stat.AcquiredConns() != p.Config().MaxConns {
		t.Fatalf("acquired = %d, want saturated pool %d", stat.AcquiredConns(), p.Config().MaxConns)
	}

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := ratelimit.ParseProxies("")
	if err != nil {
		t.Fatal(err)
	}
	logBuf := &bytes.Buffer{}
	log := slog.New(telemetry.CorrelatedHandler(slog.NewJSONHandler(logBuf, nil)))
	srv := newServer(
		&config{Addr: "127.0.0.1:0", SecureCookies: false},
		&RouterConfig{
			Pool: p, AdminPool: ap, Payments: gateway,
			Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1",
		},
		proxies, log,
	)

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve(ln)

	clientCtx, cancel := context.WithTimeout(t.Context(), storeRequestBudget+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(clientCtx, http.MethodGet, "http://"+ln.Addr().String()+"/c/audio", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("held pool status = %d, want 500: %s", resp.StatusCode, body)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("HTTP span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "GET /c/{slug}" {
		t.Errorf("span name = %q, want route template GET /c/{slug}", span.Name)
	}
	if duration := span.EndTime.Sub(span.StartTime); duration < storeRequestBudget-time.Second {
		t.Errorf("HTTP span duration %v excludes the held pool wait %v", duration, storeRequestBudget)
	}
	type completionRecord struct {
		Msg       string `json:"msg"`
		RequestID string `json:"request_id"`
		TraceID   string `json:"trace_id"`
		SpanID    string `json:"span_id"`
		Status    int    `json:"status"`
	}
	completions := 0
	for _, line := range bytes.Split(bytes.TrimSpace(logBuf.Bytes()), []byte("\n")) {
		var completion completionRecord
		if err = json.Unmarshal(line, &completion); err != nil {
			t.Fatal(err)
		}
		if completion.Msg != "request" {
			continue
		}
		completions++
		if completion.RequestID != resp.Header.Get("X-Request-ID") || completion.TraceID != span.SpanContext.TraceID().String() || completion.SpanID != span.SpanContext.SpanID().String() || completion.Status != 500 {
			t.Errorf("completion record does not identify the exported failing request: %+v", completion)
		}
	}
	if completions != 1 {
		t.Errorf("completion records = %d, want 1", completions)
	}
	var metrics metricdata.ResourceMetrics
	if err = reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	foundCanceled := false
	foundFailure := false
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "goen.db.pool.acquire_canceled" {
				for _, point := range measurement.Data.(metricdata.Gauge[int64]).DataPoints {
					if role, ok := point.Attributes.Value("db.role"); ok && role.AsString() == "store" && point.Value > 0 {
						foundCanceled = true
					}
				}
			}
			if measurement.Name == "goen.http.server.requests" {
				for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
					route, _ := point.Attributes.Value("http.route")
					status, _ := point.Attributes.Value("http.status_class")
					if route.AsString() == "GET /c/{slug}" && status.AsString() == "5xx" && point.Value == 1 {
						foundFailure = true
					}
				}
			}
		}
	}
	if !foundCanceled || !foundFailure {
		t.Errorf("exported canceled acquisition=%t and known failing HTTP request=%t, want both", foundCanceled, foundFailure)
	}
}

func TestRequestsCompleteWhenExporterUnreachable(t *testing.T) {
	shutdown, err := telemetry.Setup(t.Context(), telemetry.Config{
		Enabled:  true,
		Endpoint: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		started := time.Now()
		_ = shutdown(ctx)
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Errorf("disconnected exporter shutdown exceeded its one-second context: %v", elapsed)
		}
	})

	p, err := openPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ap, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Close()

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := ratelimit.ParseProxies("")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(
		&config{Addr: "127.0.0.1:0", SecureCookies: false},
		&RouterConfig{
			Pool: p, AdminPool: ap, Payments: gateway,
			Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1",
		},
		proxies, slog.New(slog.DiscardHandler),
	)

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve(ln)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 despite dead exporter", resp.StatusCode)
	}
}
