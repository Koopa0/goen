package payment

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCheckoutRedirectURL accepts Stripe's supported custom domains without
// weakening the browser boundary to plaintext, relative URLs or userinfo.
func TestCheckoutRedirectURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"a real session URL", "https://checkout.stripe.com/c/pay/cs_test_a1b2", true},
		{"the bare host", "https://checkout.stripe.com", true},
		{"a custom checkout domain", "https://pay.goen.example/c/pay/cs_test_a1b2", true},
		{"HTTPS is case insensitive", "HTTPS://pay.goen.example/c/pay/cs_test_a1b2", true},
		{"a nondefault https port", "https://pay.goen.example:8443/c/pay/cs_test_a1b2", true},
		{"plain http", "http://checkout.stripe.com/c/pay/x", false},
		{"userinfo pointing elsewhere", "https://checkout.stripe.com@evil.example/x", false},
		{"an empty hostname", "https://:443/c/pay/x", false},
		{"a scheme that is not a URL at all", "javascript:alert(1)", false},
		{"a protocol-relative URL", "//checkout.stripe.com/c/pay/x", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkoutRedirectURL(tt.url); got != tt.want {
				t.Errorf("checkoutRedirectURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestAnUnsafeCheckoutURLNeverRedirects(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, raw := range []string{
		"http://pay.goen.example/c/pay/cs_test_a1b2",
		"//pay.goen.example/c/pay/cs_test_a1b2",
		"https://staff@pay.goen.example/c/pay/cs_test_a1b2",
		"",
	} {
		t.Run(raw, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "https://goen.example/orders/GO-1/pay", http.NoBody,
			)
			h.toCheckout(rec, req, "GO-1", raw)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}
			if location := rec.Header().Get("Location"); location != "" {
				t.Errorf("Location = %q, want none", location)
			}
		})
	}
}

func TestACustomCheckoutDomainRedirects(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "https://goen.example/orders/GO-1/pay", http.NoBody,
	)
	const target = "https://pay.goen.example/c/pay/cs_test_a1b2"
	h.toCheckout(rec, req, "GO-1", target)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if location := rec.Header().Get("Location"); location != target {
		t.Errorf("Location = %q, want %q", location, target)
	}
}
