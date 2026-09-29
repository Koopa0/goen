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

// providerMode is the deployment's financial environment. It is a setting of
// its own: HTTPS says nothing about whether the Stripe key or the 綠界
// endpoints are real.
type providerMode string

const (
	providerSandbox providerMode = "sandbox"
	providerLive    providerMode = "live"
)

// prepareProviderPosture refuses a live deployment that would take real orders
// through a test or placeholder provider. Sandbox is as permissive as it was
// before the mode existed.
func (cfg *config) prepareProviderPosture() error {
	switch cfg.ProviderMode {
	case providerSandbox, "": // loadConfig defaults the setting to sandbox
		return nil
	case providerLive:
	default:
		return errors.New("GOEN_PROVIDER_MODE must be sandbox or live")
	}
	if !cfg.SecureCookies {
		return errors.New("GOEN_PROVIDER_MODE=live requires secure cookies; remove GOEN_INSECURE_COOKIES")
	}
	if payment.ClassifyKey(cfg.StripeAPIKey) == payment.KeyTest {
		return errors.New("GOEN_STRIPE_API_KEY is a Stripe test key while GOEN_PROVIDER_MODE is live")
	}
	if err := cfg.validateLiveInvoicing(); err != nil {
		return err
	}
	// Live reads the production store map unless told otherwise; the map has no
	// other setting that says which environment it is.
	if cfg.ECPayLogisticsBaseURL == "" {
		cfg.ECPayLogisticsBaseURL = cart.MapProductionBaseURL
	} else if namesHost(cfg.ECPayLogisticsBaseURL, cart.MapStagingBaseURL) {
		return errors.New("GOEN_ECPAY_LOGISTICS_BASE_URL names the 綠界 staging store map while GOEN_PROVIDER_MODE is live")
	}
	return cfg.validateLiveSender()
}

func (cfg *config) validateLiveInvoicing() error {
	if cfg.ECPayMerchantID == "" || cfg.ECPayHashKey == "" || cfg.ECPayHashIV == "" {
		return errors.New("GOEN_ECPAY_MERCHANT_ID, GOEN_ECPAY_HASH_KEY and GOEN_ECPAY_HASH_IV " +
			"are required while GOEN_PROVIDER_MODE is live: without them no 統一發票 is issued")
	}
	// An empty URL is the staging endpoint by default.
	if cfg.ECPayBaseURL == "" || namesHost(cfg.ECPayBaseURL, invoice.StagingBaseURL) {
		return errors.New("GOEN_ECPAY_BASE_URL must name the production invoice endpoint " +
			"while GOEN_PROVIDER_MODE is live; empty and the staging endpoint are refused")
	}
	return nil
}

func (cfg *config) validateLiveSender() error {
	from, err := mail.ParseAddress(strings.TrimSpace(cfg.SMTPFrom))
	if err != nil || !email.Valid(from.Address) {
		return errors.New("GOEN_SMTP_FROM must be a valid sender address while GOEN_PROVIDER_MODE is live")
	}
	if strings.HasSuffix(strings.ToLower(from.Address), "@goen.example") {
		return errors.New("GOEN_SMTP_FROM is the goen.example placeholder while GOEN_PROVIDER_MODE is live")
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
