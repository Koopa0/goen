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

func TestClassifyKey(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want payment.KeyMode
	}{
		{"sk_test_x", payment.KeyTest}, {"rk_test_x", payment.KeyTest}, {"rkcs_test_x", payment.KeyTest},
		{"sk_live_x", payment.KeyLive}, {"rk_live_x", payment.KeyLive},
		{"  sk_live_x\n", payment.KeyLive}, {"\tsk_test_x ", payment.KeyTest},
		{"whatever", payment.KeyUnknown}, {"", payment.KeyUnknown},
	} {
		if got := payment.ClassifyKey(tc.key); got != tc.want {
			t.Errorf("ClassifyKey(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}
