package main

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice"
)

const (
	liveKey = "rk_live_fixture"
	testKey = "sk_test_fixture"
)

// liveReady makes every setting production-shaped, with a live key.
func liveReady(c *config) {
	c.StripeAPIKey = liveKey
	c.ECPayMerchantID = "live-merchant"
	c.ECPayHashKey = "0123456789abcdef"
	c.ECPayHashIV = "fedcba9876543210"
	c.ECPayBaseURL = invoice.ProductionBaseURL
	c.SMTPFrom = "shop@merchant.example"
}

func prepare(t *testing.T, change func(*config)) (config, error) {
	t.Helper()
	t.Setenv("GOEN_BASE_URL", "https://shop.example")
	cfg := validPosture()
	change(&cfg)
	err := cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
	return cfg, err
}

// The provider posture follows the Stripe key, never the cookie setting.
func TestProviderPostureFollowsTheStripeKeyNotSecureCookies(t *testing.T) {
	// Each setting a live key refuses; a test key or no key accepts it.
	for _, tc := range []struct {
		name   string
		change func(*config)
		want   string
	}{
		{"insecure cookies", func(c *config) { c.SecureCookies = false }, "secure cookies"},
		{"no credentials", func(c *config) { c.ECPayMerchantID, c.ECPayHashKey, c.ECPayHashIV = "", "", "" }, "GOEN_ECPAY_MERCHANT_ID"},
		{"half credentials", func(c *config) { c.ECPayHashIV = "" }, "GOEN_ECPAY_HASH_IV"},
		{"implicit invoice endpoint", func(c *config) { c.ECPayBaseURL = "" }, "GOEN_ECPAY_BASE_URL"},
		{"staging invoice endpoint", func(c *config) { c.ECPayBaseURL = invoice.StagingBaseURL }, "GOEN_ECPAY_BASE_URL"},
		{"staging invoice endpoint respelled", func(c *config) { c.ECPayBaseURL = "HTTP://EINVOICE-STAGE.ecpay.com.tw/" }, "GOEN_ECPAY_BASE_URL"},
		{"staging map", func(c *config) { c.ECPayLogisticsBaseURL = cart.MapStagingBaseURL }, "GOEN_ECPAY_LOGISTICS_BASE_URL"},
		{"placeholder sender", func(c *config) { c.SMTPFrom = "goen <no-reply@goen.example>" }, "GOEN_SMTP_FROM"},
		{"unparsable sender", func(c *config) { c.SMTPFrom = "broken<>" }, "GOEN_SMTP_FROM"},
		{"empty sender", func(c *config) { c.SMTPFrom = "" }, "GOEN_SMTP_FROM"},
		{"demo account", func(c *config) {
			c.DemoAccountEmail, c.DemoAccountPassword = "demo@shop.example", "a published password"
		}, "GOEN_DEMO_ACCOUNT_EMAIL"},
		{"half a demo account", func(c *config) { c.DemoAccountPassword = "a published password" }, "GOEN_DEMO_ACCOUNT_EMAIL"},
	} {
		// An unrecognised or whitespace-padded key is judged like a live one.
		for _, key := range []string{liveKey, "sk_org_fixture", "  " + liveKey + "\n", "not-a-stripe-key"} {
			t.Run("key "+strings.TrimSpace(key)+" refuses "+tc.name, func(t *testing.T) {
				_, err := prepare(t, func(c *config) { liveReady(c); tc.change(c); c.StripeAPIKey = key })
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v, want %q", err, tc.want)
				}
				if strings.Contains(err.Error(), strings.TrimSpace(key)) {
					t.Fatal("configuration error leaked the configured key")
				}
				if strings.Contains(err.Error(), "a published password") {
					t.Fatal("configuration error leaked the demo password")
				}
			})
		}
		for _, key := range []string{testKey, "rkcs_test_fixture", " " + testKey + " ", ""} {
			t.Run("key "+key+" accepts "+tc.name, func(t *testing.T) {
				_, err := prepare(t, func(c *config) {
					liveReady(c)
					tc.change(c)
					c.StripeAPIKey = key
				})
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}

	t.Run("live key with production settings", func(t *testing.T) {
		if _, err := prepare(t, liveReady); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("sandbox keeps every staging and placeholder setting", func(t *testing.T) {
		_, err := prepare(t, func(c *config) {
			c.StripeAPIKey = testKey
			c.ECPayBaseURL = invoice.StagingBaseURL
			c.ECPayLogisticsBaseURL = cart.MapStagingBaseURL
			c.SMTPFrom = "goen <no-reply@goen.example>"
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreMapDefaultFollowsTheStripeKey(t *testing.T) {
	for _, key := range []string{liveKey, "sk_org_fixture", " " + liveKey + " "} {
		live, err := prepare(t, func(c *config) { liveReady(c); c.StripeAPIKey = key })
		if err != nil {
			t.Fatal(err)
		}
		if live.ECPayLogisticsBaseURL != cart.MapProductionBaseURL {
			t.Errorf("key %q: map URL = %q, want %q", key, live.ECPayLogisticsBaseURL, cart.MapProductionBaseURL)
		}
	}
	for _, key := range []string{testKey, " " + testKey, ""} {
		cfg, err := prepare(t, func(c *config) { c.StripeAPIKey = key })
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ECPayLogisticsBaseURL != "" {
			t.Errorf("key %q: map URL = %q, want it left to cart's staging default", key, cfg.ECPayLogisticsBaseURL)
		}
	}
}
