package payment

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// A trimmed list as Stripe returns it: two disputes that wait for the shop, one
// the bank allows no answer to, and three that do not.
const disputeListReply = `{
  "object": "list", "url": "/v1/disputes", "has_more": false,
  "data": [
    {"id": "dp_needs", "object": "dispute", "amount": 129000, "currency": "twd", "status": "needs_response",
     "payment_intent": "pi_needs", "evidence_details": {"due_by": 1791331199, "has_evidence": false, "past_due": false, "submission_count": 0}},
    {"id": "dp_warning", "object": "dispute", "amount": 5000, "currency": "twd", "status": "warning_needs_response",
     "payment_intent": "pi_warning", "evidence_details": {"due_by": 1791417599, "has_evidence": false, "past_due": false, "submission_count": 0}},
    {"id": "dp_nodeadline", "object": "dispute", "amount": 700, "currency": "twd", "status": "needs_response",
     "payment_intent": null, "evidence_details": {"due_by": 0, "has_evidence": false, "past_due": false, "submission_count": 0}},
    {"id": "dp_usd", "object": "dispute", "amount": 5000, "currency": "usd", "status": "needs_response",
     "payment_intent": null, "evidence_details": {"due_by": 1791331199}},
    {"id": "dp_review", "object": "dispute", "amount": 100, "currency": "twd", "status": "under_review",
     "payment_intent": "pi_review", "evidence_details": {"due_by": 1791331199}},
    {"id": "dp_won", "object": "dispute", "amount": 100, "currency": "twd", "status": "won",
     "payment_intent": "pi_won", "evidence_details": {"due_by": 1791331199}},
    {"id": "dp_lost", "object": "dispute", "amount": 100, "currency": "twd", "status": "lost",
     "payment_intent": "pi_lost", "evidence_details": {"due_by": 1791331199}}
  ]
}`

func disputeServer(t *testing.T, sessions map[string]string) *Gateway {
	t.Helper()
	g, _ := stripeAt(t, func(c *call) (int, string) {
		switch c.path {
		case "/v1/disputes":
			if c.query.Get("limit") != "100" {
				t.Errorf("disputes listed with limit=%q, want 100", c.query.Get("limit"))
			}
			created, err := strconv.ParseInt(c.query.Get("created[gte]"), 10, 64)
			if err != nil || time.Since(time.Unix(created, 0)) < 119*24*time.Hour || time.Since(time.Unix(created, 0)) > 121*24*time.Hour {
				t.Errorf("disputes listed with created[gte]=%q, want about 120 days ago", c.query.Get("created[gte]"))
			}
			return http.StatusOK, disputeListReply
		case "/v1/checkout/sessions":
			if id, ok := sessions[c.query.Get("payment_intent")]; ok {
				return http.StatusOK, `{"object":"list","has_more":false,"data":[{"id":"` + id + `","object":"checkout.session"}]}`
			}
			return http.StatusOK, `{"object":"list","has_more":false,"data":[]}`
		}
		return http.StatusNotFound, `{"error":{"type":"invalid_request_error","message":"no such route"}}`
	})
	return g
}

func TestDisputesNeedingResponse(t *testing.T) {
	t.Parallel()
	g := disputeServer(t, map[string]string{"pi_needs": "cs_test_needs"})
	got, err := g.DisputesNeedingResponse(t.Context())
	if err != nil {
		t.Fatalf("DisputesNeedingResponse() error = %v", err)
	}
	want := []Dispute{
		{ID: "dp_needs", AmountCents: 129000, Currency: "twd", RespondBy: time.Unix(1791331199, 0), SessionID: "cs_test_needs"},
		{ID: "dp_warning", AmountCents: 5000, Currency: "twd", RespondBy: time.Unix(1791417599, 0)},
		{ID: "dp_nodeadline", AmountCents: 700, Currency: "twd"},
		{ID: "dp_usd", AmountCents: 5000, Currency: "usd", RespondBy: time.Unix(1791331199, 0)},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DisputesNeedingResponse() mismatch (-want +got):\n%s", diff)
	}
}

func TestDisputesNeedingResponseFailsOnAStripeError(t *testing.T) {
	t.Parallel()
	g, _ := stripeAt(t, func(*call) (int, string) {
		return http.StatusInternalServerError, `{"error":{"type":"api_error","message":"down"}}`
	})
	got, err := g.DisputesNeedingResponse(t.Context())
	if err == nil {
		t.Fatalf("DisputesNeedingResponse() = %v, nil; want an error, never an empty list", got)
	}
}

func TestDisputeURL(t *testing.T) {
	t.Parallel()
	live := &Gateway{}
	test := &Gateway{sandbox: true}
	if got := live.DisputeURL("dp_1"); got != "https://dashboard.stripe.com/disputes/dp_1" {
		t.Errorf("live DisputeURL() = %q", got)
	}
	if got := test.DisputeURL("dp_1"); !strings.Contains(got, "/test/disputes/dp_1") {
		t.Errorf("sandbox DisputeURL() = %q, want the test-mode dashboard", got)
	}
}
