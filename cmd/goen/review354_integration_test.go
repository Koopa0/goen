//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
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

func TestReview354HTTPSpanCoversPoolWaitAndCompletionLog(t *testing.T) {
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
	if resp.StatusCode < 500 {
		t.Fatalf("status = %d, want saturated pool failure", resp.StatusCode)
	}
	_ = body

	completion, took, err := requestCompletionRecord(logBuf.String())
	if err != nil {
		t.Fatal(err)
	}
	if completion.traceID == "" || completion.spanID == "" {
		t.Fatalf("completion record missing trace correlation: %q", completion.line)
	}

	var httpSpan *tracetest.SpanStub
	for i := range exporter.GetSpans() {
		span := exporter.GetSpans()[i]
		if span.SpanContext.TraceID().String() == completion.traceID {
			httpSpan = &span
			break
		}
	}
	if httpSpan == nil {
		names := make([]string, len(exporter.GetSpans()))
		for i, span := range exporter.GetSpans() {
			names[i] = span.Name
		}
		t.Fatalf("expected HTTP span for completion trace_id %q, spans=%v", completion.traceID, names)
	}
	if httpSpan.Name != "GET /c/{slug}" {
		t.Fatalf("span name = %q, want route template GET /c/{slug}", httpSpan.Name)
	}
	if completion.traceID != httpSpan.SpanContext.TraceID().String() {
		t.Fatalf("completion trace_id %q != span trace_id %q", completion.traceID, httpSpan.SpanContext.TraceID().String())
	}
	if completion.spanID != httpSpan.SpanContext.SpanID().String() {
		t.Fatalf("completion span_id %q != span span_id %q", completion.spanID, httpSpan.SpanContext.SpanID().String())
	}

	spanDur := httpSpan.EndTime.Sub(httpSpan.StartTime)
	if spanDur < storeRequestBudget/2 {
		t.Fatalf("span duration %s understates pool wait; completion took=%s", spanDur, took)
	}
	if spanDur < took/2 {
		t.Fatalf("span duration %s diverges from completion took=%s", spanDur, took)
	}
}

type requestCompletion struct {
	line    string
	traceID string
	spanID  string
}

var (
	requestCompletionLine = regexp.MustCompile(`msg=request\b`)
	traceIDAttr           = regexp.MustCompile(`trace_id=([0-9a-f]+)`)
	spanIDAttr            = regexp.MustCompile(`span_id=([0-9a-f]+)`)
	tookAttr              = regexp.MustCompile(`took=([0-9.]+(?:ns|us|µs|ms|s|m|h))+`)
)

func requestCompletionRecord(logs string) (requestCompletion, time.Duration, error) {
	for line := range strings.SplitSeq(logs, "\n") {
		if line == "" || !requestCompletionLine.MatchString(line) {
			continue
		}
		traceMatch := traceIDAttr.FindStringSubmatch(line)
		spanMatch := spanIDAttr.FindStringSubmatch(line)
		if traceMatch == nil || spanMatch == nil {
			return requestCompletion{line: line}, 0, errMissingCompletionCorrelation
		}
		tookMatch := tookAttr.FindStringSubmatch(line)
		if tookMatch == nil {
			return requestCompletion{line: line}, 0, errMissingCompletionDuration
		}
		took, err := time.ParseDuration(tookMatch[1])
		if err != nil {
			return requestCompletion{line: line}, 0, err
		}
		return requestCompletion{
			line:    line,
			traceID: traceMatch[1],
			spanID:  spanMatch[1],
		}, took, nil
	}
	return requestCompletion{}, 0, errMissingCompletionRecord
}

var (
	errMissingCompletionRecord      = errors.New("no msg=request completion record in logs")
	errMissingCompletionCorrelation = errors.New("msg=request record missing trace_id or span_id")
	errMissingCompletionDuration    = errors.New("msg=request record missing took")
)
