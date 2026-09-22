//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbound"
)

type refundRecoveryPeer struct {
	mu                      sync.Mutex
	calls, creates, effects int
	key                     string
	events                  []outbound.Event
}

// Only the network destination changes; NewRefunder still constructs the real
// outbound transport, admission controls, SDK and operation deadlines.
type refundLoopbackTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (l refundLoopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.stripe.com" {
		return nil, fmt.Errorf("unexpected refund dependency %q", r.URL.Host)
	}
	local := r.Clone(r.Context())
	local.URL.Scheme, local.URL.Host = l.target.Scheme, l.target.Host
	local.Host = l.target.Host
	return l.base.RoundTrip(local)
}

func newRefundRecoveryPeer(t *testing.T) *refundRecoveryPeer {
	t.Helper()
	peer := &refundRecoveryPeer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		peer.mu.Lock()
		defer peer.mu.Unlock()
		peer.calls++
		w.Header().Set("Content-Type", "application/json")
		// Fresh connections make wire attempts correspond to SDK attempts, without
		// the HTTP transport replaying a dropped request on a reused connection.
		w.Header().Set("Connection", "close")
		var response any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_outbound_refund":
			response = map[string]any{"id": "cs_outbound_refund", "object": "checkout.session", "payment_intent": map[string]any{"id": "pi_outbound_refund"}}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds":
			if r.Form.Get("payment_intent") != "pi_outbound_refund" {
				t.Errorf("lookup intent = %q", r.Form.Get("payment_intent"))
			}
			data := []any{}
			if peer.effects != 0 {
				data = append(data, map[string]any{"id": "re_outbound_recovered", "object": "refund", "status": "succeeded", "currency": "twd", "amount": 100000,
					"payment_intent": "pi_outbound_refund", "metadata": map[string]string{"goen_request_key": peer.key}})
			}
			response = map[string]any{"object": "list", "data": data, "has_more": false, "url": "/v1/refunds"}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			peer.creates++
			key := r.Header.Get("Idempotency-Key")
			amount, err := strconv.ParseInt(r.Form.Get("amount"), 10, 64)
			if err != nil || amount != 100000 || key == "" || r.Form.Get("metadata[goen_request_key]") != key || r.Form.Get("payment_intent") != "pi_outbound_refund" {
				t.Errorf("refund lost frozen identity: key=%q form=%v", key, r.Form)
			}
			if peer.effects == 0 {
				peer.key = key
				peer.effects++
			} else if peer.key != key {
				peer.effects++
				t.Errorf("duplicate logical refund key %q after %q", key, peer.key)
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("local peer cannot drop the reply")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		default:
			t.Errorf("unexpected provider operation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport, originalRecorder := http.DefaultTransport, outbound.Recorder
	http.DefaultTransport = refundLoopbackTransport{target: target, base: originalTransport}
	outbound.Recorder = func(event outbound.Event) {
		peer.mu.Lock()
		defer peer.mu.Unlock()
		peer.events = append(peer.events, event)
	}
	t.Cleanup(func() { http.DefaultTransport = originalTransport; outbound.Recorder = originalRecorder })
	return peer
}

func (p *refundRecoveryPeer) snapshot() (calls, creates, effects int, key string, events []outbound.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.creates, p.effects, p.key, append([]outbound.Event(nil), p.events...)
}

func TestRefundHTTPRecoversCommittedOperationAfterLostReplies(t *testing.T) {
	database := dbtest.Pool(t)
	if _, err := database.Exec(t.Context(), `WITH method AS (
  INSERT INTO shipping_methods (code) VALUES ('refund_fixture') RETURNING id
 ) INSERT INTO shipping_method_versions (method_id, name, fee_cents) SELECT id, 'Refund fixture', 0 FROM method`); err != nil {
		t.Fatal(err)
	}
	actor, token := outboundRefundOperator(t, database)
	returnID := outboundRefundFixture(t, database)
	peer := newRefundRecoveryPeer(t)
	handler := outboundRefundFinancialRouter(t, database.Config().ConnString())
	path := "/admin/returns/" + returnID.String() + "/decide"
	start := time.Now()
	outboundRefundOperatorPost(t, handler, token, path, "outbound-refund-first", url.Values{"decision": {"exception"}, "resolution": {"outbound recovery"}}, "/admin/returns?refundfailed=1")
	if elapsed := time.Since(start); elapsed > 22*time.Second {
		t.Fatalf("ambiguous refund took %s, exceeds 20s operation budget plus local overhead", elapsed)
	}
	calls, creates, effects, key, events := peer.snapshot()
	if calls != 4 || creates != 2 || effects != 1 || key != "return:"+returnID.String() {
		t.Fatalf("first peer facts calls=%d creates=%d effects=%d key=%q", calls, creates, effects, key)
	}
	assertOutboundRefund(t, database, returnID, actor, "pending", key, 0, 1)
	mutations := 0
	for _, event := range events {
		if event.Class != outbound.FinancialMutation {
			continue
		}
		mutations++
		if event.Dependency != outbound.Stripe || event.Outcome != outbound.OutcomeAmbiguous || event.Attempts != 2 || event.LogicalKey != key || event.Elapsed > 21*time.Second {
			t.Fatalf("lost reply classified or bounded incorrectly: %+v", event)
		}
	}
	if mutations != 1 {
		t.Fatalf("financial operation events=%d, want one", mutations)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/returns", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "goen_session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("pending operator queue status=%d", res.Code)
	}
	retry := outboundRefundRetryForm(t, res.Body.String(), path)
	outboundRefundOperatorPost(t, handler, token, path, "outbound-refund-recover", retry, "/admin/returns?ok=1")
	assertOutboundRefund(t, database, returnID, actor, "succeeded", key, 1, 2)
	afterCalls, afterCreates, afterEffects, afterKey, afterEvents := peer.snapshot()
	if afterCalls != 6 || afterCreates != creates || afterEffects != effects || afterKey != key {
		t.Fatalf("recovery duplicated money or skipped peer: calls=%d creates=%d effects=%d key=%q", afterCalls, afterCreates, afterEffects, afterKey)
	}
	if len(afterEvents) != len(events)+2 {
		t.Fatalf("recovery did not use the two real provider lookups: %+v", afterEvents)
	}
	for _, event := range afterEvents[len(events):] {
		if event.Outcome != outbound.OutcomeSucceeded || event.Attempts != 1 {
			t.Fatalf("recovery lookup failed: %+v", event)
		}
	}
	history := outboundRefundFinancialHistory(t, database)
	outboundRefundOperatorPost(t, handler, token, path, "outbound-refund-completed", retry, "/admin/returns?refused=1")
	assertOutboundRefund(t, database, returnID, actor, "succeeded", key, 1, 2)
	if got := outboundRefundFinancialHistory(t, database); got != history {
		t.Fatal("completed replay changed audit or credit history")
	}
	finalCalls, finalCreates, finalEffects, finalKey, _ := peer.snapshot()
	if finalCalls != afterCalls || finalCreates != creates || finalEffects != effects || finalKey != key {
		t.Fatal("completed replay called provider or changed logical money")
	}
	if !strings.Contains(res.Body.String(), returnID.String()) {
		t.Fatal("pending refund missing from operator queue")
	}
}
