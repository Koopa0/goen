package payment_test

import (
	"testing"

	"github.com/koopa0/goen/internal/payment"
)

func TestGatewaySandboxRequiresAnExplicitTestKey(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"sk_test_example", true}, {"rk_test_example", true}, {"rkcs_test_example", true},
		{"sk_live_example", false}, {"rk_live_example", false}, {"example_test_key", false}, {"", false},
	} {
		t.Run(tc.key, func(t *testing.T) {
			gateway, err := payment.NewGateway(tc.key, "whsec_example", "https://goen.example")
			if err != nil {
				t.Fatal(err)
			}
			if got := gateway.Sandbox(); got != tc.want {
				t.Errorf("Sandbox() = %t, want %t", got, tc.want)
			}
		})
	}
}
