//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/payment"
)

func refundRoleStore(t *testing.T, tracer pgx.QueryTracer, name string) *payment.Store {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = tracer
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE store")
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var identity string
	if err := p.QueryRow(t.Context(), `SELECT current_user || ':' || current_setting('is_superuser')`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if identity != "store:off" {
		t.Fatalf("handler identity = %s", identity)
	}
	return payment.NewStore(p)
}

func refundOrderingIntent(t *testing.T) string {
	t.Helper()
	number, _ := order(t, 80000)
	session, intent := "cs_ordering_"+uuid.NewString(), "pi_ordering_"+uuid.NewString()
	s := payment.NewStore(pool)
	if err := s.OpenPayment(t.Context(), number, session, 80000); err != nil {
		t.Fatal(err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 80000}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT record_payment_intent_link($1, $2)`, session, intent); err != nil {
		t.Fatal(err)
	}
	return intent
}

func refundObservation(refund, intent, status string, created int64, charge bool) map[string]any {
	ev := refundWebhookEvent("evt_ordering_"+uuid.NewString(), refund, intent, 10000, status, "")
	ev["created"] = created
	ev["data"].(map[string]any)["object"].(map[string]any)["charge"] = "ch_ordering"
	if charge {
		obj := ev["data"].(map[string]any)["object"]
		ev["type"] = "charge.refunded"
		ev["data"] = map[string]any{"object": map[string]any{
			"id": "ch_ordering", "object": "charge", "payment_intent": intent,
			"refunds": map[string]any{"data": []any{obj}},
		}}
	}
	return ev
}

func signedRefundRequest(t *testing.T, ev map[string]any) *http.Request {
	t.Helper()
	body, signature := signed(t, ev)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	r.Header.Set("Stripe-Signature", signature)
	return r
}

func refundResponse(h *payment.Handler, r *http.Request) int {
	w := httptest.NewRecorder()
	h.Webhook(w, r)
	return w.Code
}

func refundHandler(t *testing.T, s *payment.Store, g *payment.Gateway) *payment.Handler {
	t.Helper()
	return payment.NewHandler(s, g, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
}

func requireRefundFact(t *testing.T, refund, status string, watermark int64) {
	t.Helper()
	var got string
	var stamp int64
	if err := pool.QueryRow(t.Context(), `SELECT status, provider_updated_at FROM stripe_refund_facts WHERE provider_ref=$1`, refund).Scan(&got, &stamp); err != nil {
		t.Fatal(err)
	}
	if got != status || stamp != watermark {
		t.Fatalf("refund fact = %s@%d; want %s@%d", got, stamp, status, watermark)
	}
}

func TestRefundCanceledAndEventOrdering(t *testing.T) {
	for _, charge := range []bool{false, true} {
		t.Run(fmt.Sprintf("charge=%t", charge), func(t *testing.T) {
			intent := refundOrderingIntent(t)
			g := refundLookupGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("ordinary ordered event performed provider I/O")
				http.Error(w, "unexpected lookup", http.StatusServiceUnavailable)
			}))
			h := refundHandler(t, refundRoleStore(t, nil, "refund-ordering"), g)
			for _, tc := range []struct {
				name     string
				statuses []string
				times    []int64
				want     string
				stamp    int64
			}{
				{"canceled", []string{"pending", "canceled"}, []int64{100, 200}, "cancelled", 200},
				{"older_pending", []string{"succeeded", "pending"}, []int64{200, 100}, "succeeded", 200},
				{"unchanged_watermark", []string{"succeeded", "succeeded", "pending"}, []int64{100, 300, 200}, "succeeded", 300},
				{"late_failure", []string{"succeeded", "failed"}, []int64{100, 200}, "failed", 200},
			} {
				t.Run(tc.name, func(t *testing.T) {
					refund := "re_ordering_" + uuid.NewString()
					for i, status := range tc.statuses {
						if code := refundResponse(h, signedRefundRequest(t, refundObservation(refund, intent, status, tc.times[i], charge))); code != http.StatusOK {
							t.Fatalf("%s HTTP=%d", status, code)
						}
					}
					requireRefundFact(t, refund, tc.want, tc.stamp)
				})
			}
		})
	}
}

type refundTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (r refundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = r.target.Scheme, r.target.Host
	cloned.URL = &u
	return r.base.RoundTrip(cloned)
}

func refundLookupGateway(t *testing.T, handler http.Handler) *payment.Gateway {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	g, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	payment.SetRefundTransport(g, refundTransport{base: srv.Client().Transport, target: u})
	return g
}

func TestRefundSameSecondLookup(t *testing.T) {
	for _, mode := range []string{"failed", "succeeded", "unavailable", "wrong_id", "wrong_intent", "wrong_amount", "wrong_currency", "wrong_charge", "malformed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			intent, refund := refundOrderingIntent(t), "re_conflict_"+uuid.NewString()
			var calls atomic.Int32
			g := refundLookupGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/refunds/"+refund {
					t.Errorf("unexpected lookup %s %s", r.Method, r.URL.Path)
				}
				if mode == "timeout" {
					<-r.Context().Done()
					return
				}
				if mode == "unavailable" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				obj := map[string]any{"id": refund, "object": "refund", "payment_intent": intent, "charge": "ch_ordering", "amount": 10000, "currency": "twd", "status": "failed", "failure_reason": "lost_or_stolen_card"}
				switch mode {
				case "succeeded":
					obj["status"] = "succeeded"
				case "wrong_id":
					obj["id"] = "re_other"
				case "wrong_intent":
					obj["payment_intent"] = "pi_other"
				case "wrong_amount":
					obj["amount"] = 9999
				case "wrong_currency":
					obj["currency"] = "usd"
				case "wrong_charge":
					obj["charge"] = "ch_other"
				case "malformed":
					obj["status"] = "unknown"
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(obj); err != nil {
					t.Error(err)
				}
			}))
			h := refundHandler(t, refundRoleStore(t, nil, "refund-conflict"), g)
			if code := refundResponse(h, signedRefundRequest(t, refundObservation(refund, intent, "succeeded", 100, false))); code != 200 {
				t.Fatalf("seed HTTP=%d", code)
			}
			if calls.Load() != 0 {
				t.Fatal("ordinary event performed provider I/O")
			}
			ev := refundObservation(refund, intent, "failed", 100, false)
			started := time.Now()
			code := refundResponse(h, signedRefundRequest(t, ev))
			if calls.Load() != 1 {
				t.Fatalf("provider calls=%d; want one with zero retries", calls.Load())
			}
			if mode == "failed" || mode == "succeeded" {
				if code != 200 {
					t.Fatalf("lookup HTTP=%d", code)
				}
				requireRefundFact(t, refund, mode, 100)
				return
			}
			if code != 500 {
				t.Fatalf("unresolved HTTP=%d; want retryable 500", code)
			}
			requireRefundFact(t, refund, "succeeded", 100)
			var claimed bool
			if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM payment_webhook_events WHERE event_id=$1)`, ev["id"]).Scan(&claimed); err != nil {
				t.Fatal(err)
			}
			if claimed {
				t.Fatal("unresolved lookup committed the event claim")
			}
			if mode == "timeout" && time.Since(started) > payment.RefundReconcileBudget+time.Second {
				t.Fatal("lookup exceeded the shared transaction budget")
			}
		})
	}
}

type refundApplyBarrier struct {
	reached chan struct{}
	release chan struct{}
	used    atomic.Bool
}

func (b *refundApplyBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT reconcile_stripe_refund_webhook(") && b.used.CompareAndSwap(false, true) {
		close(b.reached)
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*refundApplyBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestRefundLookupCannotApplyAfterAnotherWorker(t *testing.T) {
	intent, refund := refundOrderingIntent(t), "re_concurrent_"+uuid.NewString()
	seed := refundHandler(t, refundRoleStore(t, nil, "refund-seed"), enabledGateway(t))
	if code := refundResponse(seed, signedRefundRequest(t, refundObservation(refund, intent, "pending", 100, false))); code != 200 {
		t.Fatalf("seed HTTP=%d", code)
	}
	var calls atomic.Int32
	g := refundLookupGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := "succeeded"
		if calls.Add(1) > 1 {
			status = "failed"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"id": refund, "payment_intent": intent, "charge": "ch_ordering", "amount": 10000, "currency": "twd", "status": status}); err != nil {
			t.Error(err)
		}
	}))
	barrier := &refundApplyBarrier{reached: make(chan struct{}), release: make(chan struct{})}
	first := refundHandler(t, refundRoleStore(t, barrier, "refund-first"), g)
	name := "refund-second-" + uuid.NewString()
	second := refundHandler(t, refundRoleStore(t, nil, name), g)
	r1, r2 := signedRefundRequest(t, refundObservation(refund, intent, "succeeded", 100, true)), signedRefundRequest(t, refundObservation(refund, intent, "failed", 100, false))
	done1, done2 := make(chan int, 1), make(chan int, 1)
	go func() { done1 <- refundResponse(first, r1) }()
	select {
	case <-barrier.reached:
	case <-time.After(time.Second):
		t.Fatal("first did not reach apply after lookup")
	}
	go func() { done2 <- refundResponse(second, r2) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	waiting := false
	for !waiting {
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event='advisory')`, name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("second did not wait for the refund lock")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("lookup escaped lock: calls=%d", calls.Load())
	}
	close(barrier.release)
	if code := <-done1; code != 200 {
		t.Fatalf("first HTTP=%d", code)
	}
	if code := <-done2; code != 200 {
		t.Fatalf("second HTTP=%d", code)
	}
	requireRefundFact(t, refund, "failed", 100)
	if calls.Load() != 2 {
		t.Fatalf("lookup calls=%d", calls.Load())
	}
}

func TestRefundChargeLookupLimitRollsBackBatch(t *testing.T) {
	intent := refundOrderingIntent(t)
	var calls atomic.Int32
	g := refundLookupGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/v1/refunds/"), "payment_intent": intent, "charge": "ch_ordering", "amount": 10000, "currency": "twd", "status": "failed"}); err != nil {
			t.Error(err)
		}
	}))
	h := refundHandler(t, refundRoleStore(t, nil, "refund-batch"), g)
	refunds := make([]string, 0, 5)
	objects := make([]any, 0, 5)
	for range 5 {
		id := "re_batch_" + uuid.NewString()
		refunds = append(refunds, id)
		if code := refundResponse(h, signedRefundRequest(t, refundObservation(id, intent, "succeeded", 100, false))); code != 200 {
			t.Fatalf("seed HTTP=%d", code)
		}
		ev := refundObservation(id, intent, "failed", 100, false)
		objects = append(objects, ev["data"].(map[string]any)["object"])
	}
	ev := refundObservation(refunds[0], intent, "failed", 100, true)
	ev["data"].(map[string]any)["object"].(map[string]any)["refunds"] = map[string]any{"data": objects}
	if code := refundResponse(h, signedRefundRequest(t, ev)); code != 500 {
		t.Fatalf("batch HTTP=%d; want retryable 500", code)
	}
	if calls.Load() != 4 {
		t.Fatalf("lookup calls=%d; want bounded four", calls.Load())
	}
	for _, id := range refunds {
		requireRefundFact(t, id, "succeeded", 100)
	}
}

func TestRefundBackfillKeepsUnresolvedCorrectionRetryable(t *testing.T) {
	intent, refund := refundOrderingIntent(t), "re_replay_"+uuid.NewString()
	s := refundRoleStore(t, nil, "refund-backfill")
	h := refundHandler(t, s, enabledGateway(t))
	if code := refundResponse(h, signedRefundRequest(t, refundObservation(refund, intent, "succeeded", 100, false))); code != 200 {
		t.Fatalf("seed HTTP=%d", code)
	}
	ev := refundObservation(refund, intent, "failed", 100, false)
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO payment_webhook_events(provider,event_id,type,object_ref,payload,processed_at,received_at) VALUES('stripe',$1,'refund.updated',$2,$3::jsonb,now(),'2000-01-01')`, ev["id"], refund, body); err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	g := refundLookupGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !available.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"id": refund, "payment_intent": intent, "charge": "ch_ordering", "amount": 10000, "currency": "twd", "status": "failed"}); err != nil {
			t.Error(err)
		}
	}))
	if _, err := s.BackfillIgnoredRefundWebhooks(t.Context(), 1, g); err == nil {
		t.Fatal("failed lookup was swallowed")
	}
	var reconciled bool
	if err := pool.QueryRow(t.Context(), `SELECT refund_reconciled_at IS NOT NULL FROM payment_webhook_events WHERE event_id=$1`, ev["id"]).Scan(&reconciled); err != nil {
		t.Fatal(err)
	}
	if reconciled {
		t.Fatal("failed lookup marked the historical event reconciled")
	}
	requireRefundFact(t, refund, "succeeded", 100)
	available.Store(true)
	if n, err := s.BackfillIgnoredRefundWebhooks(t.Context(), 1, g); err != nil || n != 1 {
		t.Fatalf("retry=%d/%v", n, err)
	}
	requireRefundFact(t, refund, "failed", 100)
	if err := pool.QueryRow(t.Context(), `SELECT refund_reconciled_at IS NOT NULL FROM payment_webhook_events WHERE event_id=$1`, ev["id"]).Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("retry reconciled=%v/%v", reconciled, err)
	}
}
