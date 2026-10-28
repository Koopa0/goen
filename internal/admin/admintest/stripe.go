//go:build integration

package admintest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/payment/refund"
)

// StripeCreate is what the stand-in does with a refund create.
type StripeCreate int

const (
	// StripeCreateAccepted answers with a refund in StripeBehaviour.RefundStatus.
	StripeCreateAccepted StripeCreate = iota
	// StripeCreateRejected answers 400: Stripe was asked and said no.
	StripeCreateRejected
	// StripeCreateUnanswered drops the connection, as a timeout does: goen
	// cannot know what Stripe did.
	StripeCreateUnanswered
)

// StripeBehaviour is how the stand-in answers; the zero value is a Stripe that
// finds every payment intent and refunds every request in full.
type StripeBehaviour struct {
	// RefundStatus is the state an accepted refund comes back in, succeeded when
	// empty. A real one is often pending.
	RefundStatus refund.Status
	// LookupUnanswered drops the connection of the payment intent lookup, so goen
	// never gets as far as asking for a refund.
	LookupUnanswered bool
	Create           StripeCreate
}

// StripeRefund is one refund create as it arrived at Stripe.
type StripeRefund struct {
	PaymentIntent  string
	AmountCents    int64
	IdempotencyKey string
}

// Stripe stands in for Stripe's API behind a real *payment.Gateway, so the
// refund path is exercised through the SDK's own requests. It keeps no refunds
// of its own: the list of refunds already made is always empty, and every
// create it receives is recorded, answered or not.
type Stripe struct {
	t      *testing.T
	url    string
	mu     sync.Mutex
	how    StripeBehaviour
	lookup []string
	made   []StripeRefund
}

// NewStripe starts a stand-in that lasts as long as t.
func NewStripe(t *testing.T, how StripeBehaviour) *Stripe {
	t.Helper()
	s := &Stripe{t: t, how: how}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// HealthyGateway is a gateway over a stand-in that nothing inspects.
func HealthyGateway(t *testing.T) *payment.Gateway {
	t.Helper()
	return NewStripe(t, StripeBehaviour{}).Gateway()
}

// Gateway is the production gateway with its Stripe traffic sent to s.
func (s *Stripe) Gateway() *payment.Gateway {
	s.t.Helper()
	g, err := payment.NewGatewayAt("sk_test_notarealkey", "whsec_notusedbythesetests", "https://goen.example", s.url)
	if err != nil {
		s.t.Fatalf("payment.NewGatewayAt() error = %v", err)
	}
	return g
}

// Behave changes how s answers from the next request on.
func (s *Stripe) Behave(how StripeBehaviour) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.how = how
}

// Refunds is every refund create s has received, in order, answered or not.
// Local rows prove durable outcomes; only this proves a retry did not ask
// Stripe to pay twice.
func (s *Stripe) Refunds() []StripeRefund {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StripeRefund(nil), s.made...)
}

// Lookups is every checkout session whose payment intent s was asked for.
func (s *Stripe) Lookups() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lookup...)
}

func (s *Stripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	how := s.how
	s.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		session := strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")
		s.mu.Lock()
		s.lookup = append(s.lookup, session)
		s.mu.Unlock()
		if how.LookupUnanswered {
			panic(http.ErrAbortHandler)
		}
		s.reply(w, http.StatusOK, map[string]any{
			"id": session, "object": "checkout.session",
			"payment_intent": map[string]any{"id": "pi_for_" + session, "object": "payment_intent"},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds":
		s.reply(w, http.StatusOK, map[string]any{
			"object": "list", "data": []any{}, "has_more": false, "url": "/v1/refunds",
		})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
		s.createRefund(w, r, how)
	default:
		s.t.Errorf("Stripe stand-in received %s %s, which no refund path makes", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Stripe) createRefund(w http.ResponseWriter, r *http.Request, how StripeBehaviour) {
	if err := r.ParseForm(); err != nil {
		s.t.Errorf("parse the refund create form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	amount, err := strconv.ParseInt(r.PostForm.Get("amount"), 10, 64)
	if err != nil {
		s.t.Errorf("refund create amount %q: %v", r.PostForm.Get("amount"), err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	made := StripeRefund{
		PaymentIntent:  r.PostForm.Get("payment_intent"),
		AmountCents:    amount,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	}
	s.mu.Lock()
	s.made = append(s.made, made)
	s.mu.Unlock()

	switch how.Create {
	case StripeCreateUnanswered:
		panic(http.ErrAbortHandler)
	case StripeCreateRejected:
		s.reply(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"type": "invalid_request_error", "code": "charge_already_refunded",
			"message": "charge has already been refunded",
		}})
		return
	case StripeCreateAccepted:
	}

	status := how.RefundStatus
	if status == "" {
		status = refund.Succeeded
	}
	if status == refund.Cancelled {
		status = "canceled" // Stripe's spelling; the schema's is "cancelled"
	}
	s.reply(w, http.StatusOK, map[string]any{
		"id":             "re_" + made.IdempotencyKey + "_" + made.PaymentIntent[:6],
		"object":         "refund",
		"status":         string(status),
		"amount":         made.AmountCents,
		"currency":       payment.Currency,
		"payment_intent": map[string]any{"id": made.PaymentIntent, "object": "payment_intent"},
		"metadata":       metadataOf(r.PostForm),
	})
}

// metadataOf is the metadata[key]=value pairs of a form, as Stripe echoes them.
func metadataOf(form url.Values) map[string]string {
	meta := map[string]string{}
	for name, values := range form {
		if strings.HasPrefix(name, "metadata[") && strings.HasSuffix(name, "]") {
			meta[name[len("metadata["):len(name)-1]] = values[0]
		}
	}
	return meta
}

func (s *Stripe) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.t.Errorf("write the Stripe stand-in reply: %v", err)
	}
}
