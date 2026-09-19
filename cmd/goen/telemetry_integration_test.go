//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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
	log := slog.New(telemetry.CorrelatedHandler(slog.NewTextHandler(logBuf, nil)))
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

	if !strings.Contains(logBuf.String(), "request_id=") {
		t.Fatalf("request log missing request_id: %q", logBuf.String())
	}
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected an HTTP span for the catalog request")
	}
	if spans[len(spans)-1].Name != "GET /c/{slug}" {
		t.Fatalf("span name = %q, want route template GET /c/{slug}", spans[len(spans)-1].Name)
	}
	_ = body
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
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		_ = shutdown(ctx)
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

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/healthz", http.NoBody)
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
