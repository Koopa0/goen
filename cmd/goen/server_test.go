package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebhookSurvivesTheMiddlewareChain proves Stripe can reach the webhook
// through goen's CSRF defence while a cross-site browser post still cannot.
//
// goen's forms carry no CSRF token: crossOriginProtection refuses cross-site
// posts using the browser's own Sec-Fetch-Site signal instead. Stripe's webhook
// is a server-to-server POST that sends neither Sec-Fetch-Site nor Origin, and
// whether that check lets such a request through is a property of the standard
// library, not of anything in this repository.
//
// If it ever stops letting it through, every capture silently stops arriving
// and orders sit unpaid with the money already taken. That is worth exercising
// the real chain rather than trusting an assumption in a comment.
func TestWebhookSurvivesTheMiddlewareChain(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantReached bool
	}{
		{
			name:        "as Stripe sends it: no browser headers at all",
			headers:     map[string]string{"Stripe-Signature": "t=1,v1=abc"},
			wantReached: true,
		},
		{
			name:        "a same-origin browser post is allowed",
			headers:     map[string]string{"Sec-Fetch-Site": "same-origin"},
			wantReached: true,
		},
		{
			name:        "a cross-site browser post is still refused",
			headers:     map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantReached: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			mux := http.NewServeMux()
			mux.HandleFunc("POST /webhooks/stripe", func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			handler := crossOriginProtection(mux)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/webhooks/stripe", strings.NewReader(`{"id":"evt_1"}`))
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if reached != tt.wantReached {
				t.Errorf("reached handler = %v, want %v (status %d)", reached, tt.wantReached, w.Code)
			}
		})
	}
}

// TestCSPAllowsTheHandoverToStripe proves the Content-Security-Policy still
// permits the redirect that sends a customer to Stripe's card form.
//
// The payment form posts to goen and goen answers 303 to checkout.stripe.com.
// Browsers have historically applied form-action to the destination a form
// submission lands on, so 'self' alone makes paying work in some browsers and
// not in others — a failure no server-side test would ever see.
func TestCSPAllowsTheHandoverToStripe(t *testing.T) {
	var directive string
	for _, d := range strings.Split(contentSecurityPolicy, ";") {
		if strings.HasPrefix(strings.TrimSpace(d), "form-action") {
			directive = strings.TrimSpace(d)
		}
	}
	if directive == "" {
		t.Fatal("the policy has no form-action directive at all")
	}
	if !strings.Contains(directive, "https://checkout.stripe.com") {
		t.Errorf("form-action is %q; without checkout.stripe.com the redirect to "+
			"Stripe is blocked in browsers that apply form-action to redirects", directive)
	}
	if !strings.Contains(directive, "'self'") {
		t.Errorf("form-action is %q and no longer allows goen's own forms", directive)
	}
}
