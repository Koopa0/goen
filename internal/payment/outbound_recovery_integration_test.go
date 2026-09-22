//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/outbound"
	"github.com/koopa0/goen/internal/payment"
)

func TestCheckoutLostCreateReplyRecoversThroughTheSamePayment(t *testing.T) {
	ctx := t.Context()
	const amount = int64(125000)
	number, orderID := order(t, amount)
	hold(t, orderID, 0, time.Hour, "outbound-recovery:"+number)
	sessionID := "cs_outbound_" + uuid.NewString()
	wantKey := payment.SessionKey(number, amount, 0)

	var provider struct {
		sync.Mutex
		healthy bool
		keys    []string
		objects map[string]bool
	}
	provider.objects = make(map[string]bool)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			key := r.Header.Get("Idempotency-Key")
			provider.Lock()
			provider.keys = append(provider.keys, key)
			provider.objects[key] = true
			healthy := provider.healthy
			provider.Unlock()
			if !healthy {
				// The remote object exists before the TCP reply disappears.
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("drop committed create reply: %v", err)
					return
				}
				_ = conn.Close()
				return
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+sessionID:
		default:
			t.Errorf("unexpected provider operation: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"open","url":"https://checkout.stripe.com/c/pay/%s"}`, sessionID, sessionID)
	}))
	t.Cleanup(peer.Close)
	retries := int64(1)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(peer.URL), MaxNetworkRetries: &retries,
		HTTPClient: outbound.HTTPClient(outbound.Stripe),
	})
	client := stripe.NewClient("sk_test_notreal", stripe.WithBackends(&stripe.Backends{API: backend, Connect: backend, Uploads: backend}))
	gateway, err := payment.GatewayWithClient("sk_test_notreal", testWebhookSecret, "https://goen.example", client)
	if err != nil {
		t.Fatal(err)
	}
	roleConfig, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	roleConfig.MaxConns = 2
	roleConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE store")
		return roleErr
	}
	storePool, err := pgxpool.NewWithConfig(ctx, roleConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storePool.Close)
	var role string
	if err = storePool.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil || role != "store" {
		t.Fatalf("payment role=%q, error=%v", role, err)
	}
	access := cart.NewStore(storePool)
	grant := httptest.NewRecorder()
	if err = access.RememberOrder(ctx, grant, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody), number, false); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, candidate := range grant.Result().Cookies() {
		if candidate.Name == "goen_placed" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("production access grant issued no order cookie")
	}
	store := payment.NewStore(storePool)
	handler := payment.NewHandler(store, gateway, access, slog.New(slog.DiscardHandler), false)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders/{number}/pay", handler.Start)
	mux.HandleFunc("POST /webhooks/stripe", handler.Webhook)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var events []outbound.Event
	var eventMu sync.Mutex
	previousRecorder := outbound.Recorder
	outbound.Recorder = func(event outbound.Event) {
		eventMu.Lock()
		events = append(events, event)
		eventMu.Unlock()
	}
	t.Cleanup(func() { outbound.Recorder = previousRecorder })
	post := func(path string, body []byte, signature string) (int, string) {
		t.Helper()
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.AddCookie(cookie)
		if signature != "" {
			req.Header.Set("Stripe-Signature", signature)
		}
		response, callErr := httpClient.Do(req)
		if callErr != nil {
			t.Fatal(callErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode, response.Header.Get("Location")
	}
	path := "/orders/" + number + "/pay"
	if status, _ := post(path, nil, ""); status != http.StatusInternalServerError {
		t.Fatalf("lost reply status=%d, want 500", status)
	}
	provider.Lock()
	firstCalls, firstObjects := len(provider.keys), len(provider.objects)
	provider.healthy = true
	provider.Unlock()
	if firstCalls != 2 || firstObjects != 1 {
		t.Fatalf("first logical mutation attempts/remote objects=%d/%d, want 2/1", firstCalls, firstObjects)
	}
	eventMu.Lock()
	firstEvents := append([]outbound.Event(nil), events...)
	eventMu.Unlock()
	if len(firstEvents) != 1 || firstEvents[0].Outcome != outbound.OutcomeAmbiguous || firstEvents[0].Attempts != 2 || firstEvents[0].LogicalKey != wantKey || firstEvents[0].Elapsed > 21*time.Second {
		t.Fatalf("lost reply observations=%+v, want one bounded ambiguous mutation with two attempts", firstEvents)
	}
	for range 2 {
		if status, location := post(path, nil, ""); status != http.StatusSeeOther || location != "https://checkout.stripe.com/c/pay/"+sessionID {
			t.Fatalf("recovered payment response=%d %q, want same-session 303", status, location)
		}
	}
	provider.Lock()
	keys := append([]string(nil), provider.keys...)
	objects := len(provider.objects)
	provider.Unlock()
	if len(keys) != 3 || objects != 1 {
		t.Fatalf("SDK attempts after recovery=%v remote objects=%d, want three calls and one object", keys, objects)
	}
	for _, key := range keys {
		if key != wantKey {
			t.Errorf("recovery changed idempotency key to %q, want %q", key, wantKey)
		}
	}
	for _, eventID := range []string{"evt_outbound_a_" + sessionID, "evt_outbound_a_" + sessionID, "evt_outbound_b_" + sessionID} {
		payload, signature := signed(t, sessionEvent(eventID, sessionID, "paid", amount))
		if status, _ := post("/webhooks/stripe", payload, signature); status != http.StatusOK {
			t.Fatalf("settlement webhook status=%d, want 200", status)
		}
	}
	paid, err := store.Order(ctx, number)
	if err != nil || !paid.Paid {
		t.Fatalf("recovered order=%+v error=%v, want paid", paid, err)
	}
	var paymentRows, receiptRows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id=$1 AND status='succeeded' AND captured_amount_cents=$2`, orderID, amount).Scan(&paymentRows); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic='order.paid' AND dedupe_key=$1`, number).Scan(&receiptRows); err != nil {
		t.Fatal(err)
	}
	if paymentRows != 1 || receiptRows != 1 {
		t.Errorf("successful payments/receipt effects=%d/%d, want 1/1", paymentRows, receiptRows)
	}
}
