//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	stripe "github.com/stripe/stripe-go/v86"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/telemetry"
)

func TestStalledCheckoutProviderExportsOneCorrelatedTimeout(t *testing.T) {
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
	t.Cleanup(storePool.Close)
	adminPool, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	number := telemetryPaymentFixture(t)
	sessionID := "cs_telemetry_private_" + uuid.NewString()
	if err = payment.NewStore(storePool).OpenPayment(t.Context(), number, sessionID, 125000); err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/checkout/sessions/"+sessionID {
			t.Errorf("unexpected provider operation: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(peer.Close)
	previousBackend := stripe.GetBackend(stripe.APIBackend)
	retries := int64(0)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(peer.URL), MaxNetworkRetries: &retries,
	}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, previousBackend) })
	gateway, err := payment.NewGateway("sk_test_telemetry_local", "whsec_telemetry_local", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(telemetry.CorrelatedHandler(slog.NewJSONHandler(&logs, nil)))
	handler := newRouter(&RouterConfig{Pool: storePool, AdminPool: adminPool, Payments: gateway,
		Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1"}, logger)
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An inbound deadline isolates observation of a stalled provider from
		// the separate request-budget acceptance suite.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
		completed <- struct{}{}
	}))
	t.Cleanup(server.Close)
	grant := httptest.NewRecorder()
	if err = cart.NewStore(storePool).RememberOrder(t.Context(), grant,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody), number, false); err != nil {
		t.Fatal(err)
	}
	exporter.Reset()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/orders/"+number+"/pay?token=telemetry-private-token", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, cookie := range grant.Result().Cookies() {
		req.AddCookie(cookie)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	<-completed
	if response.StatusCode != http.StatusInternalServerError || providerCalls.Load() != 1 {
		t.Fatalf("status/provider attempts=%d/%d, want 500/1", response.StatusCode, providerCalls.Load())
	}
	var request, provider tracetest.SpanStub
	for _, span := range exporter.GetSpans() {
		switch span.Name {
		case "POST /orders/{number}/pay":
			request = span
		case "stripe.checkout.retrieve":
			provider = span
		}
	}
	if !request.SpanContext.IsValid() || !provider.SpanContext.IsValid() || provider.Parent.SpanID() != request.SpanContext.SpanID() || provider.SpanContext.TraceID() != request.SpanContext.TraceID() {
		t.Fatal("stalled provider has no parent payment HTTP trace")
	}
	if provider.Status.Description != "timeout" || provider.EndTime.Sub(provider.StartTime) < 50*time.Millisecond {
		t.Fatalf("provider observation excludes its wait/timeout: %+v", provider)
	}
	if !bytes.Contains(logs.Bytes(), []byte(response.Header.Get("X-Request-ID"))) || !bytes.Contains(logs.Bytes(), []byte(request.SpanContext.TraceID().String())) {
		t.Error("payment completion log is not correlated with its HTTP trace")
	}
	var metrics metricdata.ResourceMetrics
	if err = reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	var providerCount, failureCount int64
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "goen.provider.requests" {
				for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
					outcome, _ := point.Attributes.Value("provider.outcome")
					if outcome.AsString() != "timeout" {
						t.Errorf("stalled provider outcome=%s", outcome.AsString())
					}
					providerCount += point.Value
				}
			}
			if measurement.Name == "goen.http.server.requests" {
				for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
					status, _ := point.Attributes.Value("http.status_class")
					if status.AsString() == "5xx" {
						failureCount += point.Value
					}
				}
			}
		}
	}
	if providerCount != 1 || failureCount != 1 {
		t.Errorf("provider/failed request counts=%d/%d, want 1/1", providerCount, failureCount)
	}
	encoded, err := json.Marshal([]any{exporter.GetSpans(), metrics})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{sessionID, "telemetry-private-token", "123 Private Street"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("provider telemetry leaked %q", secret)
		}
	}
}

func telemetryPaymentFixture(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var methodID, versionID, orderID uuid.UUID
	code := "telemetry_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = tx.QueryRow(ctx, `INSERT INTO shipping_methods(code, destination_kind) VALUES ($1, 'address') RETURNING id`, code).Scan(&methodID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO shipping_method_versions(method_id, name, carrier, fee_cents) VALUES ($1, 'Telemetry delivery', 'Local', 0) RETURNING id`, methodID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	var number string
	if err = tx.QueryRow(ctx, `INSERT INTO orders(order_number, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
		VALUES(next_order_number(), $1, $2, 'Telemetry delivery', 0) RETURNING id, order_number`, versionID, code).Scan(&orderID, &number); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_lines(order_id, sku, product_name, unit_price_cents, quantity) VALUES($1, 'TELEMETRY', 'Telemetry fixture', 125000, 1)`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_private_data(order_id,email,recipient_name,phone,postal_code,city,district,street)
		VALUES($1,'telemetry@example.com','Telemetry','0912345678','110','Taipei','Xinyi','123 Private Street')`, orderID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return number
}
