package main

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
)

type providerMode string

const (
	providerSandbox providerMode = "sandbox"
	providerLive    providerMode = "live"
)

// Cookie security describes transport; an HTTPS demonstration can still use
// sandbox providers. Live commerce needs a separate, explicit configuration.
func (cfg *config) prepareProviderPosture() error {
	if cfg.ProviderMode == "" {
		cfg.ProviderMode = providerSandbox
	}
	if cfg.ProviderMode != providerSandbox && cfg.ProviderMode != providerLive {
		return errors.New("GOEN_PROVIDER_MODE must be sandbox or live")
	}
	if cfg.ProviderMode == providerLive && !cfg.SecureCookies {
		return errors.New("GOEN_PROVIDER_MODE=live requires secure cookies; remove GOEN_INSECURE_COOKIES")
	}
	if err := cfg.validateStripePosture(); err != nil {
		return err
	}
	if err := cfg.validateInvoicePosture(); err != nil {
		return err
	}
	return cfg.validateSenderPosture()
}

func (cfg *config) validateStripePosture() error {
	if cfg.StripeAPIKey == "" {
		return nil
	}
	prefixes := []string{"sk_test_", "rk_test_"}
	if cfg.ProviderMode == providerLive {
		prefixes = []string{"sk_live_", "rk_live_"}
	}
	if !strings.HasPrefix(cfg.StripeAPIKey, prefixes[0]) && !strings.HasPrefix(cfg.StripeAPIKey, prefixes[1]) {
		return errors.New("GOEN_STRIPE_API_KEY must match GOEN_PROVIDER_MODE (sandbox test key or live key)")
	}
	return nil
}

func (cfg *config) validateInvoicePosture() error {
	if cfg.ECPayMerchantID == "" && cfg.ECPayHashKey == "" && cfg.ECPayHashIV == "" {
		return nil
	}
	endpoint := strings.TrimRight(cfg.ECPayBaseURL, "/")
	if cfg.ProviderMode == providerLive {
		if endpoint != invoice.ProductionBaseURL {
			return errors.New("GOEN_ECPAY_BASE_URL must explicitly name the production invoice endpoint in live mode")
		}
		if cfg.ECPayMerchantID == "2000132" {
			return errors.New("GOEN_ECPAY_MERCHANT_ID is the published staging merchant, not a live merchant")
		}
		return nil
	}
	if endpoint == invoice.ProductionBaseURL {
		return errors.New("GOEN_ECPAY_BASE_URL names production while GOEN_PROVIDER_MODE is sandbox")
	}
	return nil
}

func (cfg *config) validateSenderPosture() error {
	if cfg.SMTPAddr == "" {
		return nil
	}
	from, err := mail.ParseAddress(strings.TrimSpace(cfg.SMTPFrom))
	if err != nil || !email.Valid(from.Address) {
		return errors.New("GOEN_SMTP_FROM must be a valid sender address")
	}
	if cfg.ProviderMode == providerLive && strings.HasSuffix(strings.ToLower(from.Address), "@goen.example") {
		return errors.New("GOEN_SMTP_FROM must replace the goen.example placeholder in live mode")
	}
	return nil
}
