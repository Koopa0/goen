package payment

import "testing"

// TestCheckoutHostOnlyAcceptsStripe proves the redirect guard that gosec's taint
// analyser cannot see through.
//
// If it ever accepts something else, goen's checkout becomes a redirect to an
// attacker's page that asks a customer — mid-purchase, expecting exactly this —
// for a card number.
func TestCheckoutHostOnlyAcceptsStripe(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"a real session URL", "https://checkout.stripe.com/c/pay/cs_test_a1b2", true},
		{"the bare host", "https://checkout.stripe.com", true},
		{"plain http", "http://checkout.stripe.com/c/pay/x", false},
		{"another stripe subdomain", "https://dashboard.stripe.com/x", false},
		{"userinfo pointing elsewhere", "https://checkout.stripe.com@evil.example/x", false},
		// These two are the cases that separate an exact match from a suffix
		// match. Both END with "checkout.stripe.com" and neither IS it, so a
		// strings.HasSuffix implementation accepts them — and a table without
		// such a case stays green under exactly that mutation.
		{"a host merely ending in the real one", "https://evilcheckout.stripe.com/x", false},
		{"a subdomain of the real one", "https://x.checkout.stripe.com/y", false},
		{"a longer host that contains it", "https://checkout.stripe.com.evil.example/x", false},
		{"an entirely different host", "https://evil.example/c/pay/cs_test_a1b2", false},
		{"a scheme that is not a URL at all", "javascript:alert(1)", false},
		{"a protocol-relative URL", "//checkout.stripe.com/c/pay/x", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkoutHost(tt.url); got != tt.want {
				t.Errorf("checkoutHost(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
