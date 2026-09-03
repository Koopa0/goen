package admin

// The HTTP interface to Stripe: what goen puts on the wire, and what it makes
// of the answers. White-box so the SDK's backend can point at an httptest.Server.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

type refundStripeCall struct {
	method      string
	path        string
	form        url.Values
	idempotency string
	apiVersion  string
	contextDone <-chan struct{}
}

// refundStripeAt returns a StripeRefunder talking to h instead of
// api.stripe.com. Retries are off so every request is visible exactly once.
func refundStripeAt(t *testing.T, h func(*refundStripeCall) (int, string)) (StripeRefunder, *[]refundStripeCall) {
	t.Helper()
	var log []refundStripeCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse Stripe request form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		call := refundStripeCall{
			method:      r.Method,
			path:        r.URL.Path,
			form:        r.Form.Clone(),
			idempotency: r.Header.Get("Idempotency-Key"),
			apiVersion:  r.Header.Get("Stripe-Version"),
			contextDone: r.Context().Done(),
		}
		log = append(log, call)
		status, reply := h(&call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)

	noRetries := int64(0)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL), MaxNetworkRetries: &noRetries,
	})
	const testKey = "sk_test_notarealkey"
	return StripeRefunder{
		client: stripe.NewClient(testKey, stripe.WithBackends(&stripe.Backends{
			API: backend, Connect: backend, Uploads: backend,
		})),
	}, &log
}

func refundStripeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal Stripe fixture: %v", err)
	}
	return string(b)
}

func validStripeRefundFixture(
	id, status, requestKey, paymentIntentID string, amount int64,
) map[string]any {
	return map[string]any{
		"id":       id,
		"object":   "refund",
		"status":   status,
		"amount":   amount,
		"currency": "twd",
		"payment_intent": map[string]any{
			"id": paymentIntentID, "object": "payment_intent",
		},
		"metadata": map[string]string{refundKeyTag: requestKey},
	}
}

func TestStripeRefunderExpandsTheCheckoutPaymentIntent(t *testing.T) {
	r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
		return http.StatusOK, `{
			"id":"cs_test_order_7",
			"object":"checkout.session",
			"payment_intent":{"id":"pi_customer_paid_7","object":"payment_intent"}
		}`
	})

	got, err := r.PaymentIntentFor(t.Context(), "cs_test_order_7")
	if err != nil {
		t.Fatalf("PaymentIntentFor() error = %v", err)
	}
	if got != "pi_customer_paid_7" {
		t.Errorf("PaymentIntentFor() = %q, want pi_customer_paid_7", got)
	}
	if len(*log) != 1 {
		t.Fatalf("made %d Stripe requests, want exactly 1", len(*log))
	}
	sent := (*log)[0]
	if sent.method != http.MethodGet || sent.path != "/v1/checkout/sessions/cs_test_order_7" {
		t.Errorf("sent %s %s, want GET /v1/checkout/sessions/cs_test_order_7",
			sent.method, sent.path)
	}
	if got := sent.form["expand[0]"]; len(got) != 1 || got[0] != "payment_intent" {
		t.Errorf("form[expand[0]] = %v, want [payment_intent]", got)
	}
	if sent.apiVersion != "2026-08-26.dahlia" {
		t.Errorf("Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
			sent.apiVersion)
	}
}

func TestStripeRefunderRejectsInvalidCheckoutAndPaymentIntentIdentities(t *testing.T) {
	for _, tt := range []struct {
		name      string
		sessionID string
		intentID  string
	}{
		{name: "empty returned session", sessionID: "", intentID: "pi_good"},
		{name: "different returned session", sessionID: "cs_someone_else", intentID: "pi_good"},
		{name: "empty payment intent", sessionID: "cs_requested", intentID: ""},
		{name: "oversized payment intent", sessionID: "cs_requested", intentID: strings.Repeat("x", 256)},
		{name: "whitespace payment intent", sessionID: "cs_requested", intentID: " \t"},
		{name: "control in payment intent", sessionID: "cs_requested", intentID: "pi_good\nforged"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := refundStripeAt(t, func(*refundStripeCall) (int, string) {
				return http.StatusOK, refundStripeJSON(t, map[string]any{
					"id": tt.sessionID, "object": "checkout.session",
					"payment_intent": map[string]any{
						"id": tt.intentID, "object": "payment_intent",
					},
				})
			})
			intent, err := r.PaymentIntentFor(t.Context(), "cs_requested")
			if err == nil {
				t.Fatal("PaymentIntentFor() accepted an invalid provider identity")
			}
			if intent != "" {
				t.Errorf("PaymentIntentFor() = %q, want empty", intent)
			}
		})
	}
}

func TestStripeRefunderRejectsInvalidLocalProviderIdentitiesBeforeCallingStripe(t *testing.T) {
	r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
		t.Fatal("an invalid identity reached Stripe")
		return http.StatusInternalServerError, `{}`
	})
	if _, err := r.PaymentIntentFor(t.Context(), " \t"); err == nil {
		t.Fatal("PaymentIntentFor() accepted an invalid session id")
	}
	if _, _, err := r.Refund(t.Context(), strings.Repeat("x", 256), "return:key", 100); err == nil {
		t.Fatal("Refund() accepted an invalid payment intent id")
	}
	if len(*log) != 0 {
		t.Errorf("made %d Stripe calls, want none", len(*log))
	}
}

func TestStripeRefunderFindsAnExistingRequestAcrossRefundPages(t *testing.T) {
	const (
		intentID   = "pi_paid_order_9"
		requestKey = "return:request-9"
	)
	page := 0
	r, log := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		page++
		switch page {
		case 1:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":true,
				"data":[{
					"id":"re_newer_other_request","object":"refund","status":"succeeded",
					"metadata":{"goen_request_key":"return:someone-else"}
				}]
			}`
		case 2:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":false,
				"data":[{
					"id":"re_original_request_9","object":"refund","status":"pending",
					"amount":8750,"currency":"twd",
					"payment_intent":{"id":"pi_paid_order_9","object":"payment_intent"},
					"metadata":{"goen_request_key":"return:request-9"}
				}]
			}`
		default:
			t.Errorf("unexpected Stripe request %d: %s %s", page, call.method, call.path)
			return http.StatusInternalServerError, `{"error":{"message":"unexpected request"}}`
		}
	})

	id, state, err := r.Refund(t.Context(), intentID, requestKey, 8750)
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	if id != "re_original_request_9" || state != RefundPending {
		t.Errorf("Refund() = (%q, %q), want (re_original_request_9, pending)", id, state)
	}
	if len(*log) != 2 {
		t.Fatalf("made %d Stripe requests, want 2 list pages and no create", len(*log))
	}
	for i, sent := range *log {
		if sent.method != http.MethodGet || sent.path != "/v1/refunds" {
			t.Errorf("request %d sent %s %s, want GET /v1/refunds", i+1, sent.method, sent.path)
		}
		if got := sent.form.Get("payment_intent"); got != intentID {
			t.Errorf("request %d payment_intent = %q, want %q", i+1, got, intentID)
		}
		if sent.apiVersion != "2026-08-26.dahlia" {
			t.Errorf("request %d Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
				i+1, sent.apiVersion)
		}
	}
	if got := (*log)[0].form.Get("starting_after"); got != "" {
		t.Errorf("first page starting_after = %q, want empty", got)
	}
	if got := (*log)[1].form.Get("starting_after"); got != "re_newer_other_request" {
		t.Errorf("second page starting_after = %q, want re_newer_other_request", got)
	}
}

func TestStripeRefunderRefusesMultipleProviderRefundsForOneRequestKey(t *testing.T) {
	r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
		return http.StatusOK, `{
			"object":"list","url":"/v1/refunds","has_more":false,
			"data":[
				{"id":"re_duplicate_a","object":"refund","status":"succeeded",
				 "amount":100,"currency":"twd",
				 "payment_intent":{"id":"pi_paid","object":"payment_intent"},
				 "metadata":{"goen_request_key":"return:duplicate"}},
				{"id":"re_duplicate_b","object":"refund","status":"pending",
				 "amount":100,"currency":"twd",
				 "payment_intent":{"id":"pi_paid","object":"payment_intent"},
				 "metadata":{"goen_request_key":"return:duplicate"}}
			]
		}`
	})

	id, state, err := r.Refund(t.Context(), "pi_paid", "return:duplicate", 100)
	if err == nil {
		t.Fatal("Refund() silently attributed one of two provider refunds")
	}
	if id != "" || state != "" {
		t.Errorf("Refund() = (%q, %q, %v), want no arbitrarily selected provider fact",
			id, state, err)
	}
	if errors.Is(err, ErrRefundCreateRejected) {
		t.Errorf("duplicate lookup = %v, must remain ambiguous rather than free the claim", err)
	}
	if len(*log) != 1 {
		t.Errorf("made %d Stripe calls, want one list and no create", len(*log))
	}
}

func TestStripeRefunderRejectsMismatchedListedRefundFacts(t *testing.T) {
	const (
		intentID   = "pi_paid"
		requestKey = "return:request"
		amount     = int64(100)
	)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "wrong amount", mutate: func(ref map[string]any) { ref["amount"] = int64(99) }},
		{name: "missing amount", mutate: func(ref map[string]any) { delete(ref, "amount") }},
		{name: "wrong payment intent", mutate: func(ref map[string]any) {
			ref["payment_intent"] = map[string]any{
				"id": "pi_someone_else", "object": "payment_intent",
			}
		}},
		{name: "missing payment intent", mutate: func(ref map[string]any) {
			delete(ref, "payment_intent")
		}},
		{name: "wrong currency", mutate: func(ref map[string]any) { ref["currency"] = "usd" }},
		{name: "missing currency", mutate: func(ref map[string]any) { delete(ref, "currency") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := validStripeRefundFixture(
				"re_existing", "pending", requestKey, intentID, amount,
			)
			tt.mutate(ref)
			r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
				return http.StatusOK, refundStripeJSON(t, map[string]any{
					"object": "list", "url": "/v1/refunds", "has_more": false,
					"data": []any{ref},
				})
			})

			id, state, err := r.Refund(t.Context(), intentID, requestKey, amount)
			if err == nil {
				t.Fatal("Refund() accepted a listed refund with mismatched provider facts")
			}
			if id != "" || state != "" {
				t.Errorf("Refund() = (%q, %q, %v), want no provider values", id, state, err)
			}
			if errors.Is(err, ErrRefundCreateRejected) {
				t.Errorf("listed refund mismatch = %v, must remain ambiguous", err)
			}
			if len(*log) != 1 {
				t.Errorf("made %d Stripe calls, want one list and no create", len(*log))
			}
		})
	}
}

func TestStripeRefunderRejectsInvalidListedRefundIdentities(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("x", 256), " \t", "re_good\nforged"} {
		t.Run(id, func(t *testing.T) {
			r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
				return http.StatusOK, refundStripeJSON(t, map[string]any{
					"object": "list", "url": "/v1/refunds", "has_more": false,
					"data": []any{map[string]any{
						"id": id, "object": "refund", "status": "pending",
						"metadata": map[string]string{refundKeyTag: "return:request"},
					}},
				})
			})
			gotID, state, err := r.Refund(t.Context(), "pi_good", "return:request", 100)
			if err == nil {
				t.Fatal("Refund() accepted an invalid listed refund id")
			}
			if gotID != "" || state != "" {
				t.Errorf("Refund() = (%q, %q, %v), want no provider values", gotID, state, err)
			}
			if len(*log) != 1 {
				t.Errorf("made %d Stripe calls, want one list and no create", len(*log))
			}
		})
	}
}

func TestStripeRefunderCreatesTheRequestedRefundIdempotently(t *testing.T) {
	request := 0
	r, log := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		request++
		switch request {
		case 1:
			return http.StatusOK, `{
				"object":"list","url":"/v1/refunds","has_more":false,"data":[]
			}`
		case 2:
			return http.StatusOK, `{
				"id":"re_created_12","object":"refund","status":"succeeded",
				"amount":12850,"currency":"twd",
				"payment_intent":{"id":"pi_paid_order_12","object":"payment_intent"},
				"metadata":{"goen_request_key":"return:request-12"}
			}`
		default:
			t.Errorf("unexpected Stripe request %d: %s %s", request, call.method, call.path)
			return http.StatusInternalServerError, `{"error":{"message":"unexpected request"}}`
		}
	})

	id, state, err := r.Refund(t.Context(), "pi_paid_order_12", "return:request-12", 12850)
	if err != nil {
		t.Fatalf("Refund() error = %v", err)
	}
	if id != "re_created_12" || state != RefundSucceeded {
		t.Errorf("Refund() = (%q, %q), want (re_created_12, succeeded)", id, state)
	}
	if len(*log) != 2 {
		t.Fatalf("made %d Stripe requests, want one list and one create", len(*log))
	}

	listed, created := (*log)[0], (*log)[1]
	if listed.method != http.MethodGet || listed.path != "/v1/refunds" {
		t.Errorf("first request sent %s %s, want GET /v1/refunds", listed.method, listed.path)
	}
	if got := listed.form.Get("payment_intent"); got != "pi_paid_order_12" {
		t.Errorf("list payment_intent = %q, want pi_paid_order_12", got)
	}
	if created.method != http.MethodPost || created.path != "/v1/refunds" {
		t.Errorf("second request sent %s %s, want POST /v1/refunds", created.method, created.path)
	}
	want := map[string]string{
		"payment_intent":             "pi_paid_order_12",
		"amount":                     "12850",
		"metadata[goen_request_key]": "return:request-12",
	}
	for key, value := range want {
		if got := created.form.Get(key); got != value {
			t.Errorf("create form[%s] = %q, want %q", key, got, value)
		}
	}
	if created.idempotency != "return:request-12" {
		t.Errorf("create Idempotency-Key = %q, want return:request-12", created.idempotency)
	}
	if listed.idempotency != "" {
		t.Errorf("list Idempotency-Key = %q, want empty", listed.idempotency)
	}
	if created.apiVersion != "2026-08-26.dahlia" {
		t.Errorf("create Stripe-Version = %q, want the API reviewed with stripe-go v86.4",
			created.apiVersion)
	}
}

func TestStripeRefunderRejectsMismatchedCreatedRefundFacts(t *testing.T) {
	const (
		intentID   = "pi_paid"
		requestKey = "return:request"
		amount     = int64(100)
	)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "wrong amount", mutate: func(ref map[string]any) { ref["amount"] = int64(99) }},
		{name: "missing amount", mutate: func(ref map[string]any) { delete(ref, "amount") }},
		{name: "wrong payment intent", mutate: func(ref map[string]any) {
			ref["payment_intent"] = map[string]any{
				"id": "pi_someone_else", "object": "payment_intent",
			}
		}},
		{name: "missing payment intent", mutate: func(ref map[string]any) {
			delete(ref, "payment_intent")
		}},
		{name: "wrong currency", mutate: func(ref map[string]any) { ref["currency"] = "usd" }},
		{name: "missing currency", mutate: func(ref map[string]any) { delete(ref, "currency") }},
		{name: "wrong request key", mutate: func(ref map[string]any) {
			ref["metadata"] = map[string]string{refundKeyTag: "return:someone-else"}
		}},
		{name: "missing request key", mutate: func(ref map[string]any) {
			delete(ref, "metadata")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := validStripeRefundFixture(
				"re_created", "succeeded", requestKey, intentID, amount,
			)
			tt.mutate(ref)
			request := 0
			r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
				request++
				if request == 1 {
					return http.StatusOK,
						`{"object":"list","url":"/v1/refunds","has_more":false,"data":[]}`
				}
				return http.StatusOK, refundStripeJSON(t, ref)
			})

			id, state, err := r.Refund(t.Context(), intentID, requestKey, amount)
			if err == nil {
				t.Fatal("Refund() accepted a created refund with mismatched provider facts")
			}
			if id != "" || state != "" {
				t.Errorf("Refund() = (%q, %q, %v), want no provider values", id, state, err)
			}
			if errors.Is(err, ErrRefundCreateRejected) {
				t.Errorf("created refund mismatch = %v, must remain ambiguous", err)
			}
			if len(*log) != 2 {
				t.Errorf("made %d Stripe calls, want one list and one create", len(*log))
			}
		})
	}
}

func TestOnlyARefundCreateRejectionGetsTheStableRejectionSentinel(t *testing.T) {
	const rejection = `{"error":{"type":"invalid_request_error",` +
		`"code":"charge_already_refunded","message":"already refunded"}}`

	t.Run("the preliminary list failed", func(t *testing.T) {
		r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
			return http.StatusBadRequest, rejection
		})
		_, _, err := r.Refund(t.Context(), "pi_paid", "return:list-failed", 100)
		if err == nil {
			t.Fatal("Refund() accepted a failed provider lookup")
		}
		if errors.Is(err, ErrRefundCreateRejected) {
			t.Errorf("list error = %v, must not mean the refund CREATE was rejected", err)
		}
		if len(*log) != 1 {
			t.Errorf("made %d calls, want only the failed list", len(*log))
		}
	})

	t.Run("the create endpoint rejected", func(t *testing.T) {
		request := 0
		r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
			request++
			if request == 1 {
				return http.StatusOK,
					`{"object":"list","url":"/v1/refunds","has_more":false,"data":[]}`
			}
			return http.StatusBadRequest, rejection
		})
		_, _, err := r.Refund(t.Context(), "pi_paid", "return:create-rejected", 100)
		if !errors.Is(err, ErrRefundCreateRejected) {
			t.Errorf("create error = %v, want ErrRefundCreateRejected", err)
		}
		if _, ok := errors.AsType[*stripe.Error](err); !ok {
			t.Errorf("create error = %v, lost Stripe's provider cause", err)
		}
		if len(*log) != 2 {
			t.Errorf("made %d calls, want one list and one create", len(*log))
		}
	})
}

func TestStripeRefunderRejectsInvalidCreatedRefundIdentities(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("x", 256), " \t", "re_good\nforged"} {
		t.Run(id, func(t *testing.T) {
			request := 0
			r, log := refundStripeAt(t, func(*refundStripeCall) (int, string) {
				request++
				if request == 1 {
					return http.StatusOK, `{
						"object":"list","url":"/v1/refunds","has_more":false,"data":[]
					}`
				}
				return http.StatusOK, refundStripeJSON(t, map[string]any{
					"id": id, "object": "refund", "status": "succeeded", "amount": int64(100),
					"currency": "twd",
					"payment_intent": map[string]any{
						"id": "pi_good", "object": "payment_intent",
					},
					"metadata": map[string]string{refundKeyTag: "return:request"},
				})
			})
			gotID, state, err := r.Refund(t.Context(), "pi_good", "return:request", 100)
			if err == nil {
				t.Fatal("Refund() accepted an invalid created refund id")
			}
			if gotID != "" || state != "" {
				t.Errorf("Refund() = (%q, %q, %v), want no provider values", gotID, state, err)
			}
			if len(*log) != 2 {
				t.Errorf("made %d Stripe calls, want one list and one create", len(*log))
			}
		})
	}
}

func TestStripeRefunderCancelsTheHTTPCallWithItsContext(t *testing.T) {
	started := make(chan struct{})
	r, _ := refundStripeAt(t, func(call *refundStripeCall) (int, string) {
		close(started)
		<-call.contextDone
		return http.StatusRequestTimeout, `{"error":{"message":"request canceled"}}`
	})

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := r.PaymentIntentFor(ctx, "cs_test_cancel_me")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Stripe request did not reach the test server")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("PaymentIntentFor() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PaymentIntentFor did not return after its context was canceled")
	}
}
