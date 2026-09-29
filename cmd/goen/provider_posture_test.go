package main

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice"
)

// liveReady is a configuration every live refusal is measured against.
func liveReady(c *config) {
	c.ProviderMode = providerLive
	c.StripeAPIKey = "rk_live_fixture"
	c.ECPayMerchantID = "live-merchant"
	c.ECPayHashKey = "0123456789abcdef"
	c.ECPayHashIV = "fedcba9876543210"
	c.ECPayBaseURL = invoice.ProductionBaseURL
	c.SMTPFrom = "shop@merchant.example"
}

func TestProviderModeIsIndependentOfSecureCookies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config)
		want   string // "" means the configuration is accepted
	}{
		// Sandbox stays as permissive as it was before the mode existed.
		{"https sandbox", func(c *config) {}, ""},
		{"sandbox sk test key", func(c *config) { c.StripeAPIKey = "sk_test_fixture" }, ""},
		{"sandbox rkcs test key", func(c *config) { c.StripeAPIKey = "rkcs_test_fixture" }, ""},
		{"sandbox live key", func(c *config) { c.StripeAPIKey = "sk_live_fixture" }, ""},
		{"sandbox no invoicing", func(c *config) {}, ""},
		{"sandbox staging invoice", func(c *config) { c.ECPayBaseURL = invoice.StagingBaseURL }, ""},
		{"sandbox staging map", func(c *config) { c.ECPayLogisticsBaseURL = cart.MapStagingBaseURL }, ""},
		{"sandbox placeholder sender", func(c *config) { c.SMTPFrom = "goen <no-reply@goen.example>" }, ""},
		{"sandbox production endpoints", func(c *config) {
			c.ECPayBaseURL = invoice.ProductionBaseURL
			c.ECPayLogisticsBaseURL = cart.MapProductionBaseURL
		}, ""},
		{"unknown mode", func(c *config) { c.ProviderMode = "guess" }, "GOEN_PROVIDER_MODE"},

		{"live ready", liveReady, ""},
		{"live insecure cookies", func(c *config) { liveReady(c); c.SecureCookies = false }, "secure cookies"},
		{"live sk test key", func(c *config) { liveReady(c); c.StripeAPIKey = "sk_test_fixture" }, "GOEN_STRIPE_API_KEY"},
		{"live rk test key", func(c *config) { liveReady(c); c.StripeAPIKey = "rk_test_fixture" }, "GOEN_STRIPE_API_KEY"},
		{"live rkcs test key", func(c *config) { liveReady(c); c.StripeAPIKey = "rkcs_test_fixture" }, "GOEN_STRIPE_API_KEY"},
		{"live no credentials", func(c *config) {
			liveReady(c)
			c.ECPayMerchantID, c.ECPayHashKey, c.ECPayHashIV = "", "", ""
		}, "GOEN_ECPAY_MERCHANT_ID"},
		{"live half credentials", func(c *config) { liveReady(c); c.ECPayHashIV = "" }, "GOEN_ECPAY_HASH_IV"},
		{"live implicit invoice endpoint", func(c *config) { liveReady(c); c.ECPayBaseURL = "" }, "GOEN_ECPAY_BASE_URL"},
		{"live staging invoice endpoint", func(c *config) { liveReady(c); c.ECPayBaseURL = invoice.StagingBaseURL }, "GOEN_ECPAY_BASE_URL"},
		{"live staging invoice endpoint respelled", func(c *config) { liveReady(c); c.ECPayBaseURL = "HTTP://EINVOICE-STAGE.ecpay.com.tw/" }, "GOEN_ECPAY_BASE_URL"},
		{"live staging map", func(c *config) { liveReady(c); c.ECPayLogisticsBaseURL = cart.MapStagingBaseURL }, "GOEN_ECPAY_LOGISTICS_BASE_URL"},
		{"live placeholder sender", func(c *config) { liveReady(c); c.SMTPFrom = "goen <no-reply@goen.example>" }, "GOEN_SMTP_FROM"},
		{"live unparsable sender", func(c *config) { liveReady(c); c.SMTPFrom = "broken<>" }, "GOEN_SMTP_FROM"},
		{"live empty sender", func(c *config) { liveReady(c); c.SMTPFrom = "" }, "GOEN_SMTP_FROM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", "https://shop.example")
			cfg := validPosture()
			cfg.ProviderMode = providerSandbox
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

func TestLiveStoreMapDefaultsToProductionAndSandboxToStaging(t *testing.T) {
	t.Setenv("GOEN_BASE_URL", "https://shop.example")
	live := validPosture()
	liveReady(&live)
	if err := live.prepareRuntimePosture(slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if live.ECPayLogisticsBaseURL != cart.MapProductionBaseURL {
		t.Errorf("live map URL = %q, want %q", live.ECPayLogisticsBaseURL, cart.MapProductionBaseURL)
	}
	sandbox := validPosture()
	sandbox.ProviderMode = providerSandbox
	if err := sandbox.prepareRuntimePosture(slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if sandbox.ECPayLogisticsBaseURL != "" {
		t.Errorf("sandbox map URL = %q, want it left to cart's staging default", sandbox.ECPayLogisticsBaseURL)
	}
}
