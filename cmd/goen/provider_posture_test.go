package main

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/invoice"
)

func TestProviderModeIsIndependentOfSecureCookies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config)
		want   string
	}{
		{"https sandbox", func(c *config) { c.StripeAPIKey = "rk_test_fixture"; c.ECPayMerchantID = "2000132" }, ""},
		{"live explicit", func(c *config) {
			c.ProviderMode = providerLive
			c.StripeAPIKey = "rk_live_fixture"
			c.ECPayMerchantID = "live-merchant"
			c.ECPayBaseURL = invoice.ProductionBaseURL
			c.SMTPFrom = "shop@merchant.example"
		}, ""},
		{"unknown mode", func(c *config) { c.ProviderMode = "guess" }, "GOEN_PROVIDER_MODE"},
		{"test key in live", func(c *config) { c.ProviderMode = providerLive; c.StripeAPIKey = "rk_test_fixture" }, "GOEN_STRIPE_API_KEY"},
		{"live key in sandbox", func(c *config) { c.StripeAPIKey = "sk_live_fixture" }, "GOEN_STRIPE_API_KEY"},
		{"live implicit invoice endpoint", func(c *config) { c.ProviderMode = providerLive; c.ECPayMerchantID = "merchant" }, "GOEN_ECPAY_BASE_URL"},
		{"live staging invoice endpoint", func(c *config) {
			c.ProviderMode = providerLive
			c.ECPayMerchantID = "merchant"
			c.ECPayBaseURL = invoice.StagingBaseURL
		}, "GOEN_ECPAY_BASE_URL"},
		{"staging merchant with live endpoint", func(c *config) {
			c.ProviderMode = providerLive
			c.ECPayMerchantID = "2000132"
			c.ECPayBaseURL = invoice.ProductionBaseURL
		}, "GOEN_ECPAY_MERCHANT_ID"},
		{"sandbox production endpoint", func(c *config) { c.ECPayMerchantID = "merchant"; c.ECPayBaseURL = invoice.ProductionBaseURL }, "GOEN_ECPAY_BASE_URL"},
		{"live placeholder sender", func(c *config) { c.ProviderMode = providerLive }, "GOEN_SMTP_FROM"},
		{"malformed sandbox sender", func(c *config) { c.SMTPFrom = "broken<>" }, "GOEN_SMTP_FROM"},
		{"insecure live", func(c *config) { c.ProviderMode = providerLive; c.SecureCookies = false }, "secure cookies"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", "https://shop.example")
			cfg := validPosture()
			tc.change(&cfg)
			err := cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
			if err != nil && cfg.StripeAPIKey != "" && strings.Contains(err.Error(), cfg.StripeAPIKey) {
				t.Fatal("configuration error leaked the configured key")
			}
		})
	}
}
