package payment

import (
	"errors"
	"testing"
)

// TestGatewayRefundsDisabled: PaymentIntentFor and Refund on a gateway without
// a key answer ErrDisabled. A gateway built without a Stripe key holds no
// client; both refund methods must answer ErrDisabled instead of dereferencing
// it.
func TestGatewayRefundsDisabled(t *testing.T) {
	g, err := NewGateway("", "", "https://goen.test")
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if _, err := g.PaymentIntentFor(t.Context(), "cs_test_order_7"); !errors.Is(err, ErrDisabled) {
		t.Errorf("PaymentIntentFor() error = %v, want ErrDisabled", err)
	}
	if id, state, err := g.Refund(t.Context(), "pi_paid", "return:key", 100); !errors.Is(err, ErrDisabled) || id != "" || state != "" {
		t.Errorf("Refund() = (%q, %q, %v), want ErrDisabled and no values", id, state, err)
	}
}
