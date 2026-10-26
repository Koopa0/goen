package health

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/payment"
)

// stripeReplies is what the stand-in Stripe answers: the dispute list, the
// checkout sessions of each payment intent, and how long each reply takes.
type stripeReplies struct {
	disputes string
	sessions map[string]string // payment intent to session id
	status   int               // of every reply; 0 is 200
	delay    time.Duration
	block    bool // hold every request until the caller gives up
}

// gatewayOver returns a real Gateway whose Stripe traffic goes to an httptest
// server answering as r says.
func gatewayOver(t *testing.T, apiKey string, r stripeReplies) *payment.Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.block {
			<-req.Context().Done()
			return
		}
		time.Sleep(r.delay)
		w.Header().Set("Content-Type", "application/json")
		if r.status != 0 {
			w.WriteHeader(r.status)
			_, _ = io.WriteString(w, `{"error":{"type":"api_error","message":"stripe down"}}`)
			return
		}
		switch req.URL.Path {
		case "/v1/disputes":
			_, _ = io.WriteString(w, r.disputes)
		case "/v1/checkout/sessions":
			list := `{"object":"list","has_more":false,"data":[]}`
			if id, ok := r.sessions[req.URL.Query().Get("payment_intent")]; ok {
				list = `{"object":"list","has_more":false,"data":[{"id":"` + id + `","object":"checkout.session"}]}`
			}
			_, _ = io.WriteString(w, list)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"no such route"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	g, err := payment.NewGatewayAt(apiKey, "whsec_notusedbythesetests", "https://goen.example", srv.URL)
	if err != nil {
		t.Fatalf("NewGatewayAt() error = %v", err)
	}
	return g
}

const testKey = "sk_test_notarealkey"

var quiet = slog.New(slog.DiscardHandler)

func noOrders(context.Context, []string) (map[string]string, error) { return nil, nil }

// A dispute list as Stripe returns it, trimmed: two disputes that wait for the
// shop, one of them paid through checkout session cs_1, and one that does not.
const openAndWonDisputes = `{"object":"list","url":"/v1/disputes","has_more":false,"data":[
  {"id":"dp_1","object":"dispute","amount":129000,"currency":"twd","status":"needs_response",
   "payment_intent":"pi_1","evidence_details":{"due_by":1793491199}},
  {"id":"dp_2","object":"dispute","amount":700,"currency":"twd","status":"warning_needs_response",
   "payment_intent":"pi_unknown","evidence_details":{"due_by":0}},
  {"id":"dp_won","object":"dispute","amount":100,"currency":"twd","status":"won",
   "payment_intent":"pi_won","evidence_details":{"due_by":1793491199}}
]}`

const oneDispute = `{"object":"list","has_more":false,"data":[
  {"id":"dp_1","object":"dispute","amount":100,"currency":"twd","status":"needs_response",
   "payment_intent":"pi_1","evidence_details":{"due_by":0}}]}`

// TestReadDisputesMatchesOrders: readDisputes matches orders and formats the
// deadline in shop time. Each dispute's checkout session is matched to its
// order number, a session with no order stays without one, a dispute Stripe
// has settled is not listed, and the response deadline reads in the shop's zone.
func TestReadDisputesMatchesOrders(t *testing.T) {
	t.Parallel()
	g := gatewayOver(t, testKey, stripeReplies{
		disputes: openAndWonDisputes, sessions: map[string]string{"pi_1": "cs_1"},
	})
	orders := func(_ context.Context, ids []string) (map[string]string, error) {
		if diff := cmp.Diff([]string{"cs_1"}, ids); diff != "" {
			t.Errorf("sessions asked of the database (-want +got):\n%s", diff)
		}
		return map[string]string{"cs_1": "GN-100"}, nil
	}
	got := readDisputes(t.Context(), g, time.Second, quiet, orders)
	// 2026-10-31 23:59:59 UTC is 2026-11-01 07:59 in Taipei.
	want := DisputeState{Configured: true, Items: []OpenDispute{
		{
			URL: "https://dashboard.stripe.com/test/disputes/dp_1", OrderNumber: "GN-100",
			AmountCents: 129000, Currency: "twd", RespondBy: "2026-11-01 07:59",
		},
		{URL: "https://dashboard.stripe.com/test/disputes/dp_2", AmountCents: 700, Currency: "twd"},
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("readDisputes() mismatch (-want +got):\n%s", diff)
	}
}

// TestReadDisputesLinksToTheDashboard: A test key links to Stripe's test-mode
// dashboard and a live key to the live one.
func TestReadDisputesLinksToTheDashboard(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct{ key, want string }{
		"test key": {testKey, "https://dashboard.stripe.com/test/disputes/dp_1"},
		"live key": {"sk_live_notarealkey", "https://dashboard.stripe.com/disputes/dp_1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := gatewayOver(t, tt.key, stripeReplies{disputes: oneDispute, sessions: map[string]string{}})
			got := readDisputes(t.Context(), g, time.Second, quiet, noOrders)
			if len(got.Items) != 1 || got.Items[0].URL != tt.want {
				t.Errorf("readDisputes() = %+v, want one dispute linked to %s", got, tt.want)
			}
		})
	}
}

// TestReadDisputesStripeUnavailable: readDisputes is unknown when Stripe fails
// or times out. The state is configured and unknown, lists nothing, and does
// not count as healthy.
func TestReadDisputesStripeUnavailable(t *testing.T) {
	t.Parallel()
	for name, replies := range map[string]stripeReplies{
		"error":   {status: http.StatusInternalServerError},
		"timeout": {block: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := gatewayOver(t, testKey, replies)
			got := readDisputes(t.Context(), g, 100*time.Millisecond, quiet, noOrders)
			if !got.Configured || !got.Unknown || len(got.Items) != 0 {
				t.Errorf("readDisputes() = %+v, want Configured and Unknown", got)
			}
			if got.Healthy() {
				t.Error("an unread Stripe counted as healthy")
			}
		})
	}
}

// TestReadDisputesNoStripe: readDisputes without a Stripe key leaves the row
// out. The state is unconfigured and healthy, whether the gateway is missing or
// has no key.
func TestReadDisputesNoStripe(t *testing.T) {
	t.Parallel()
	keyless, err := payment.NewGateway("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGateway() without a key: %v", err)
	}
	for name, g := range map[string]*payment.Gateway{"nil gateway": nil, "no key": keyless} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := readDisputes(t.Context(), g, time.Second, quiet, noOrders)
			if got.Configured || !got.Healthy() {
				t.Errorf("readDisputes() = %+v, want an unconfigured, healthy state", got)
			}
		})
	}
}

// TestReadDisputesOrderLookupFails: readDisputes flags a failed order lookup
// instead of claiming there is no order. The dispute is still listed,
// OrdersUnknown is set, and the state does not count as healthy.
func TestReadDisputesOrderLookupFails(t *testing.T) {
	t.Parallel()
	g := gatewayOver(t, testKey, stripeReplies{disputes: oneDispute, sessions: map[string]string{"pi_1": "cs_1"}})
	got := readDisputes(t.Context(), g, time.Second, quiet,
		func(context.Context, []string) (map[string]string, error) { return nil, errors.New("db down") })
	if got.Unknown || !got.OrdersUnknown || len(got.Items) != 1 || got.Items[0].OrderNumber != "" {
		t.Errorf("readDisputes() = %+v, want the dispute listed with OrdersUnknown", got)
	}
	if got.Healthy() {
		t.Error("a failed order lookup counted as healthy")
	}
}

// TestReadDisputesOrderLookupOwnTime: readDisputes gives the order lookup its
// own time after a slow Stripe. The two Stripe reads take 240ms of a 400ms
// budget; the lookup must still be given the whole of its own.
func TestReadDisputesOrderLookupOwnTime(t *testing.T) {
	t.Parallel()
	const budget = 400 * time.Millisecond
	g := gatewayOver(t, testKey, stripeReplies{
		disputes: oneDispute, sessions: map[string]string{"pi_1": "cs_1"}, delay: 120 * time.Millisecond,
	})
	var left time.Duration
	got := readDisputes(t.Context(), g, budget, quiet,
		func(ctx context.Context, _ []string) (map[string]string, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("the order lookup has no deadline")
			}
			left = time.Until(deadline)
			return map[string]string{"cs_1": "GN-1"}, nil
		})
	if got.Unknown || got.OrdersUnknown || len(got.Items) != 1 || got.Items[0].OrderNumber != "GN-1" {
		t.Fatalf("readDisputes() = %+v, want the order found", got)
	}
	if left < budget*3/4 {
		t.Errorf("the order lookup had %v left, want close to its own %v", left, budget)
	}
}

// TestDisputeState_HealthyOpenDispute: an open dispute is not healthy. A
// dispute waiting for a response does not count as healthy.
func TestDisputeState_HealthyOpenDispute(t *testing.T) {
	t.Parallel()
	state := DisputeState{Configured: true, Items: []OpenDispute{{}}}
	if state.Healthy() {
		t.Error("a dispute waiting for a response counted as healthy")
	}
}
