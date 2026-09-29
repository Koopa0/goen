package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

const validTOTPKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func validPosture() config {
	return config{
		SecureCookies: true,
		TOTPKey:       validTOTPKey,
		SMTPAddr:      "smtp.example:587",
		BaseURL:       "https://shop.example",
	}
}

func TestLoadConfigPrefersStripeAPIKeyAndAcceptsTheLegacyName(t *testing.T) {
	t.Setenv("GOEN_DATABASE_URL", "postgres://store.example/goen")
	t.Setenv("GOEN_LOG_LEVEL", "info")

	for _, tt := range []struct {
		name, current, legacy, want string
	}{
		{name: "current name", current: "rk_test_current", want: "rk_test_current"},
		{name: "legacy fallback", legacy: "sk_test_legacy", want: "sk_test_legacy"},
		{
			name: "current wins", current: "rk_test_current", legacy: "sk_test_legacy",
			want: "rk_test_current",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_STRIPE_API_KEY", tt.current)
			t.Setenv("GOEN_STRIPE_SECRET_KEY", tt.legacy)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if cfg.StripeAPIKey != tt.want {
				t.Errorf("StripeAPIKey = %q, want %q", cfg.StripeAPIKey, tt.want)
			}
		})
	}
}

// TestLocalConfigurationIsPreparedBeforeExternalDependencies keeps a bad
// deployment from touching its database before all local posture and provider
// configuration is known to be usable. The malformed database URL would be
// the first error in every row if run reordered those operations.
func TestLocalConfigurationIsPreparedBeforeExternalDependencies(t *testing.T) {
	tests := []struct {
		name          string
		totpKey       string
		smtpAddr      string
		smtpUser      string
		trustedProxy  string
		stripeKey     string
		stripeWebhook string
		want          string
	}{
		{name: "posture", smtpAddr: "smtp.example:587", want: "GOEN_TOTP_KEY"},
		{
			name: "mailer", totpKey: validTOTPKey, smtpAddr: "not-a-host-port",
			smtpUser: "user", want: "GOEN_SMTP_ADDR",
		},
		{
			name: "trusted proxies", totpKey: validTOTPKey, smtpAddr: "smtp.example:587",
			trustedProxy: "not-a-cidr", want: "GOEN_TRUSTED_PROXIES",
		},
		{
			name: "provider", totpKey: validTOTPKey, smtpAddr: "smtp.example:587",
			trustedProxy: "127.0.0.1/32", stripeKey: "sk_test_configured",
			want: "webhook secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_DATABASE_URL", "%not-a-database-url")
			t.Setenv("GOEN_LOG_LEVEL", "info")
			t.Setenv("GOEN_INSECURE_COOKIES", "")
			t.Setenv("GOEN_TOTP_KEY", tt.totpKey)
			t.Setenv("GOEN_SMTP_ADDR", tt.smtpAddr)
			t.Setenv("GOEN_SMTP_USER", tt.smtpUser)
			t.Setenv("GOEN_BASE_URL", "https://shop.example")
			t.Setenv("GOEN_TRUSTED_PROXIES", tt.trustedProxy)
			t.Setenv("GOEN_STRIPE_API_KEY", "")
			t.Setenv("GOEN_STRIPE_SECRET_KEY", tt.stripeKey)
			t.Setenv("GOEN_STRIPE_WEBHOOK_SECRET", tt.stripeWebhook)
			t.Setenv("GOEN_ECPAY_MERCHANT_ID", "")
			t.Setenv("GOEN_ECPAY_HASH_KEY", "")
			t.Setenv("GOEN_ECPAY_HASH_IV", "")
			t.Setenv("GOEN_GOOGLE_CLIENT_ID", "")
			t.Setenv("GOEN_GOOGLE_CLIENT_SECRET", "")

			err := run()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("run error = %v, want local %q refusal before database parsing", err, tt.want)
			}
		})
	}
}

// TestStartupRefusesATOTPKeyThatIsNotAKey keeps format validation outside the
// production-only gate and binds each refusal to the setting that caused it.
func TestStartupRefusesATOTPKeyThatIsNotAKey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		secure  bool
		wantErr bool
		wantKey bool
	}{
		{name: "missing production key", secure: true, wantErr: true},
		{name: "missing development key", secure: false},
		{name: "production passphrase", raw: "a-key-from-the-environment", secure: true, wantErr: true},
		{name: "development passphrase", raw: "a-key-from-the-environment", secure: false, wantErr: true},
		{name: "development key", raw: validTOTPKey, secure: false, wantKey: true},
		{name: "production key", raw: validTOTPKey, secure: true, wantKey: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", "https://shop.example")
			cfg := validPosture()
			cfg.SecureCookies = tt.secure
			cfg.TOTPKey = tt.raw
			err := cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "GOEN_TOTP_KEY") {
					t.Fatalf("prepareRuntimePosture error = %v, want GOEN_TOTP_KEY refusal", err)
				}
				if tt.raw != "" && strings.Contains(err.Error(), tt.raw) {
					t.Errorf("startup error leaked the configured key: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareRuntimePosture: %v", err)
			}
			if got := len(cfg.totpKey); tt.wantKey && got != 32 {
				t.Errorf("parsed key length = %d, want 32", got)
			} else if !tt.wantKey && cfg.totpKey != nil {
				t.Errorf("missing key parsed as %x, want nil", cfg.totpKey)
			}
		})
	}
}

// TestAProductionPostureRefusesAnUnconfiguredMailer pins both production
// refusal and the development warning. Earlier settings are valid in every row
// so neither can answer for the SMTP rule.
func TestAProductionPostureRefusesAnUnconfiguredMailer(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		secure   bool
		wantErr  bool
		wantWarn bool
	}{
		{name: "missing in production", secure: true, wantErr: true},
		{name: "missing in development", secure: false, wantWarn: true},
		{name: "configured in production", addr: "smtp.example:587", secure: true},
		{name: "configured in development", addr: "smtp.example:587", secure: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", "https://shop.example")
			cfg := validPosture()
			cfg.SMTPAddr = tt.addr
			cfg.SecureCookies = tt.secure
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))
			err := cfg.prepareRuntimePosture(log)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "GOEN_SMTP_ADDR") {
					t.Fatalf("prepareRuntimePosture error = %v, want GOEN_SMTP_ADDR refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareRuntimePosture: %v", err)
			}
			if warned := strings.Contains(logs.String(), "GOEN_SMTP_ADDR"); warned != tt.wantWarn {
				t.Errorf("SMTP warning=%v, want %v; log=%s", warned, tt.wantWarn, logs.String())
			}
		})
	}
}

// TestProductionPostureRefusesAnUnsafeBaseURL covers the posture-specific half
// of SiteOrigin: shape in every environment, TLS in production, and one
// canonical value handed downstream.
func TestProductionPostureRefusesAnUnsafeBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		secure    bool
		wantError string
		wantURL   string
	}{
		{name: "cleartext production", baseURL: "http://shop.example", secure: true, wantError: "plain HTTP"},
		{name: "not an origin", baseURL: "httpx://evil.example/deep/path?q=1", secure: true, wantError: "not an origin"},
		{name: "canonical production origin", baseURL: "https://shop.example/", secure: true, wantURL: "https://shop.example"},
		{name: "cleartext development", baseURL: "http://127.0.0.1:9700", secure: false, wantURL: "http://127.0.0.1:9700"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", tt.baseURL)
			cfg := validPosture()
			cfg.BaseURL = tt.baseURL
			cfg.SecureCookies = tt.secure
			err := cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("prepareRuntimePosture error = %v, want %q", err, tt.wantError)
				}
				if tt.wantError == "plain HTTP" && !strings.Contains(err.Error(), "https") {
					t.Errorf("cleartext refusal does not name https: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareRuntimePosture: %v", err)
			}
			if cfg.BaseURL != tt.wantURL {
				t.Errorf("canonical BaseURL = %q, want %q", cfg.BaseURL, tt.wantURL)
			}
		})
	}
}

// TestPostureReportsTheFirstBrokenDependency holds the mandated diagnostic
// order: TOTP, then SMTP, then the origin. A later arm must never hide the
// first configuration mistake.
func TestPostureReportsTheFirstBrokenDependency(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want string
	}{
		{name: "TOTP first", cfg: config{SecureCookies: true, BaseURL: "bad"}, want: "GOEN_TOTP_KEY"},
		{name: "SMTP second", cfg: config{SecureCookies: true, TOTPKey: validTOTPKey, BaseURL: "bad"}, want: "GOEN_SMTP_ADDR"},
		{name: "BaseURL third", cfg: config{SecureCookies: true, TOTPKey: validTOTPKey, SMTPAddr: "smtp:587", BaseURL: "bad"}, want: "not an origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOEN_BASE_URL", "bad")
			err := tt.cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("prepareRuntimePosture error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestAMalformedSMTPAddressIsFatalAndNeverTheLogSender keeps a configured
// authenticated relay from degrading into the sender whose nil means
// delivered, while retaining that sender as an explicit development fallback.
func TestAMalformedSMTPAddressIsFatalAndNeverTheLogSender(t *testing.T) {
	logBuffer := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logBuffer, nil))

	bad, err := newSender(&config{SMTPAddr: "not-a-host-port", SMTPUser: "u"}, log)
	if err == nil || !strings.Contains(err.Error(), "GOEN_SMTP_ADDR") {
		t.Errorf("newSender malformed error = %v, want GOEN_SMTP_ADDR", err)
	}
	if _, silent := bad.(email.LogSender); silent {
		t.Error("a configured malformed relay degraded to email.LogSender")
	}
	if bad != nil {
		t.Errorf("newSender malformed returned %T, want nil sender", bad)
	}
	if _, err = newNotifier(&config{
		SMTPAddr: "not-a-host-port", SMTPUser: "u", BaseURL: "https://goen.test",
	}, log); err == nil || !strings.Contains(err.Error(), "GOEN_SMTP_ADDR") {
		t.Errorf("newNotifier malformed error = %v, want GOEN_SMTP_ADDR", err)
	}

	configured, err := newSender(&config{
		SMTPAddr: "smtp.example:587", SMTPFrom: "goen@example.com",
		SMTPUser: "u", SMTPPassword: "p",
	}, log)
	if err != nil {
		t.Fatalf("newSender configured: %v", err)
	}
	smtpSender, ok := configured.(email.SMTPSender)
	if !ok || smtpSender.TLSName != "smtp.example" {
		t.Errorf("configured sender = %#v, want SMTPSender with TLSName smtp.example", configured)
	}

	development, err := newSender(&config{}, log)
	if err != nil {
		t.Fatalf("newSender development: %v", err)
	}
	logSender, ok := development.(email.LogSender)
	if !ok || logSender.ShowBody {
		t.Errorf("development sender = %#v, want body-hiding LogSender", development)
	}
	if logBuffer.Len() != 0 {
		t.Errorf("newNotifier duplicated the posture warning: %s", logBuffer.String())
	}

	// Without authentication there is no host extracted for PlainAuth. The
	// dialer will report a malformed address to the outbox, which reschedules it
	// instead of falsely stamping delivery.
	unauthenticated, err := newSender(&config{SMTPAddr: "not-a-host-port"}, log)
	if err != nil {
		t.Fatalf("unauthenticated sender was rejected early: %v", err)
	}
	if _, ok := unauthenticated.(email.SMTPSender); !ok {
		t.Errorf("unauthenticated sender = %T, want SMTPSender", unauthenticated)
	}
}
