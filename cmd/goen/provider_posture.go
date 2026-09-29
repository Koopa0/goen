package main

import (
	"errors"
	"net/mail"
	"net/url"
	"strings"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/payment"
)

// prepareProviderPosture refuses a configuration that holds a live Stripe key
// beside a staging or placeholder setting, which would take real orders through
// a test provider. The posture follows the key alone, read by
// [payment.ClassifyKey]: a test, unknown or absent key is sandbox, and sandbox
// is as permissive as it was before this check existed. Secure cookies say
// nothing about it: an HTTPS demonstration can run on test keys.
func (cfg *config) prepareProviderPosture() error {
	if payment.ClassifyKey(cfg.StripeAPIKey) != payment.KeyLive {
		return nil
	}
	if !cfg.SecureCookies {
		return errors.New("GOEN_STRIPE_API_KEY is a live key, which requires secure cookies; remove GOEN_INSECURE_COOKIES")
	}
	if err := cfg.validateLiveInvoicing(); err != nil {
		return err
	}
	// A live key reads the production store map unless told otherwise; the map
	// has no other setting that says which environment it is.
	if cfg.ECPayLogisticsBaseURL == "" {
		cfg.ECPayLogisticsBaseURL = cart.MapProductionBaseURL
	} else if namesHost(cfg.ECPayLogisticsBaseURL, cart.MapStagingBaseURL) {
		return errors.New("GOEN_ECPAY_LOGISTICS_BASE_URL names the 綠界 staging store map beside a live GOEN_STRIPE_API_KEY")
	}
	return cfg.validateLiveSender()
}

func (cfg *config) validateLiveInvoicing() error {
	if cfg.ECPayMerchantID == "" || cfg.ECPayHashKey == "" || cfg.ECPayHashIV == "" {
		return errors.New("GOEN_ECPAY_MERCHANT_ID, GOEN_ECPAY_HASH_KEY and GOEN_ECPAY_HASH_IV " +
			"are required beside a live GOEN_STRIPE_API_KEY: without them no 統一發票 is issued")
	}
	// An empty URL is the staging endpoint by default.
	if cfg.ECPayBaseURL == "" || namesHost(cfg.ECPayBaseURL, invoice.StagingBaseURL) {
		return errors.New("GOEN_ECPAY_BASE_URL must name the production invoice endpoint " +
			"beside a live GOEN_STRIPE_API_KEY; empty and the staging endpoint are refused")
	}
	return nil
}

func (cfg *config) validateLiveSender() error {
	from, err := mail.ParseAddress(strings.TrimSpace(cfg.SMTPFrom))
	if err != nil || !email.Valid(from.Address) {
		return errors.New("GOEN_SMTP_FROM must be a valid sender address beside a live GOEN_STRIPE_API_KEY")
	}
	if strings.HasSuffix(strings.ToLower(from.Address), "@goen.example") {
		return errors.New("GOEN_SMTP_FROM is the goen.example placeholder beside a live GOEN_STRIPE_API_KEY")
	}
	return nil
}

// namesHost reports whether raw points at the same host as reference, however
// the scheme, case or trailing slash is spelled.
func namesHost(raw, reference string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	ref, refErr := url.Parse(reference)
	return err == nil && refErr == nil && strings.EqualFold(u.Hostname(), ref.Hostname())
}
