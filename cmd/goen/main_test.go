package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
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
		demoEmail     string
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
		{
			name: "half a demo account", totpKey: validTOTPKey, smtpAddr: "smtp.example:587",
			trustedProxy: "127.0.0.1/32", demoEmail: "demo@shop.example",
			want: "GOEN_DEMO_ACCOUNT_PASSWORD",
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
			t.Setenv("GOEN_DEMO_ACCOUNT_EMAIL", tt.demoEmail)
			t.Setenv("GOEN_DEMO_ACCOUNT_PASSWORD", "")

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

// TestAListenAddressWithNoHostIsNotGuessedAsTheOrigin: ":9701" is the usual Go
// listen address, and "http://" + it would put a host-less origin in every
// Stripe return URL, emailed link and sitemap location.
func TestAListenAddressWithNoHostIsNotGuessedAsTheOrigin(t *testing.T) {
	t.Setenv("GOEN_DATABASE_URL", "postgres://store.example/goen")
	t.Setenv("GOEN_LOG_LEVEL", "info")
	t.Setenv("GOEN_INSECURE_COOKIES", "1")
	t.Setenv("GOEN_BASE_URL", "")

	for _, addr := range []string{":9701", "0.0.0.0:9701", "[::]:9701"} {
		t.Run(addr, func(t *testing.T) {
			t.Setenv("GOEN_ADDR", addr)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			err = cfg.prepareRuntimePosture(slog.New(slog.DiscardHandler))
			if err == nil {
				t.Fatalf("GOEN_ADDR=%s started with the origin %q", addr, cfg.BaseURL)
			}
			for _, want := range []string{"GOEN_ADDR", "GOEN_BASE_URL"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not name %s: %v", want, err)
				}
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

// TestATrustListNoPeerOfTheListenerCanMatchIsRefused keeps a proxy list from
// starting that believes nobody. A loopback listener is reached only from this
// host's loopback, so a list without a loopback address never reads
// X-Forwarded-For, and every visitor shares the proxy's one rate-limit bucket.
// Where the listener's peers cannot be known, nothing is refused.
func TestATrustListNoPeerOfTheListenerCanMatchIsRefused(t *testing.T) {
	t.Parallel()

	const private = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
	for _, tt := range []struct {
		name    string
		addr    string
		trusted string
		refused bool
	}{
		{name: "IPv4 loopback, private networks only", addr: "127.0.0.1:9700", trusted: private, refused: true},
		{name: "IPv4 loopback, IPv6 loopback only", addr: "127.0.0.1:9700", trusted: "::1", refused: true},
		{name: "IPv4 loopback, same-host proxy", addr: "127.0.0.1:9700", trusted: "127.0.0.1,::1"},
		{name: "IPv4 loopback, the loopback network", addr: "127.0.0.2:9700", trusted: "127.0.0.0/8"},
		{name: "IPv4 loopback, a mapped spelling", addr: "[::ffff:127.0.0.1]:9700", trusted: "127.0.0.1"},
		{name: "IPv6 loopback, IPv4 loopback only", addr: "[::1]:9700", trusted: "127.0.0.1", refused: true},
		{name: "IPv6 loopback, same-host proxy", addr: "[::1]:9700", trusted: "::1"},
		{name: "localhost, private networks only", addr: "localhost:9700", trusted: private, refused: true},
		{name: "localhost, IPv6 loopback only", addr: "localhost:9700", trusted: "::1", refused: true},
		{name: "localhost, IPv4 loopback", addr: "localhost:9700", trusted: "127.0.0.1"},
		{name: "localhost in capitals, same-host proxy", addr: "LOCALHOST:9700", trusted: "127.0.0.1,::1"},
		{name: "every interface", addr: ":9700", trusted: private},
		{name: "IPv4 wildcard", addr: "0.0.0.0:9700", trusted: private},
		{name: "IPv6 wildcard", addr: "[::]:9700", trusted: private},
		{name: "an interface address", addr: "10.0.0.5:9700", trusted: private},
		{name: "a host name", addr: "goen.internal:9700", trusted: "127.0.0.1"},
		{name: "loopback with nothing trusted keeps the warning", addr: "127.0.0.1:9700", trusted: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config{Addr: tt.addr, TrustedProxies: tt.trusted, SecureCookies: true}
			proxies, err := cfg.trustedProxies(slog.New(slog.DiscardHandler))
			if !tt.refused {
				if err != nil {
					t.Fatalf("GOEN_ADDR=%s GOEN_TRUSTED_PROXIES=%q refused: %v", tt.addr, tt.trusted, err)
				}
				if proxies == nil {
					t.Fatal("accepted without a proxy set")
				}
				return
			}
			if err == nil {
				t.Fatalf("GOEN_ADDR=%s GOEN_TRUSTED_PROXIES=%q started; no peer of that "+
					"listener is trusted, so every visitor shares one bucket", tt.addr, tt.trusted)
			}
			for _, want := range []string{"GOEN_TRUSTED_PROXIES", "GOEN_ADDR", "127.0.0.1,::1"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %s", err, want)
				}
			}
		})
	}
}

// TestTheDemoManifestTrustsItsOwnProxy runs the deployment template's pair
// through the same startup check, so the template cannot drift back into a
// list its listener never meets.
func TestTheDemoManifestTrustsItsOwnProxy(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "demo", "manifest.env"))
	if err != nil {
		t.Fatalf("read the demo manifest: %v", err)
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if name, value, ok := strings.Cut(line, "="); ok {
			values[name] = value
		}
	}
	cfg := config{
		Addr: values["GOEN_ADDR"], TrustedProxies: values["GOEN_TRUSTED_PROXIES"], SecureCookies: true,
	}
	if cfg.Addr == "" || cfg.TrustedProxies == "" {
		t.Fatalf("the manifest sets GOEN_ADDR=%q and GOEN_TRUSTED_PROXIES=%q; TLS terminates "+
			"in front of the demo, so both must be set", cfg.Addr, cfg.TrustedProxies)
	}
	if _, err := cfg.trustedProxies(slog.New(slog.DiscardHandler)); err != nil {
		t.Errorf("the demo manifest would not start: %v", err)
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

// TestEveryTopicGoenEnqueuesHasAHandler: a message whose topic has no handler is
// rescheduled for ever and fails nothing, so the mail it stands for silently
// never leaves. The topics are read from the outbox package's source, so one is
// held from the day it is declared rather than from the day somebody lists it.
func TestEveryTopicGoenEnqueuesHasAHandler(t *testing.T) {
	t.Parallel()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	log := slog.New(slog.DiscardHandler)
	outboxStore := newOutboxStore(workerDeps{
		pool: idle, admin: idle, maintenance: idle, log: log, invoices: unconfiguredInvoicing(t),
	})

	topics := declaredTopics(t)
	if len(topics) < 13 {
		t.Fatalf("read %d topics from internal/outbox, want at least 13; the source scan is not "+
			"seeing the constants, and the check below would pass on nothing", len(topics))
	}
	for name, topic := range topics {
		if !handled(t, outboxStore, topic) {
			t.Errorf("outbox.%s (%q) has no handler in newOutboxStore; every message on it is "+
				"rescheduled for ever", name, topic)
		}
	}
}

// TestAnInvoiceDueReachesTheClaimWithoutAProvider: with no 加值中心 the
// handler still claims, so the operation waits for one to be configured instead
// of the message being stamped delivered with nothing filed.
func TestAnInvoiceDueReachesTheClaimWithoutAProvider(t *testing.T) {
	t.Parallel()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err = invoiceDueHandler(idle, unconfiguredInvoicing(t))(ctx, &outbox.InvoiceDue{
		OrderNumber: "GO-261002-000001", Trigger: "commit:GO-261002-000001",
	})
	if err == nil {
		t.Fatal("invoice.due was reported done without reaching the database; with no " +
			"加值中心 the sale's invoice is dropped and a provider configured later files nothing")
	}
}

// unconfiguredInvoicing is the gateway a deployment with no 加值中心 runs with.
func unconfiguredInvoicing(t *testing.T) *invoice.Gateway {
	t.Helper()
	g, err := invoice.NewGateway("", "", "", "")
	if err != nil || g.Enabled() {
		t.Fatalf("an empty 加值中心 configuration = %v, enabled %t; want legal and off", err, g.Enabled())
	}
	return g
}

// declaredTopics is every outbox.Topic* variable, by name, with its stored name.
func declaredTopics(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "internal", "outbox", "*.go"))
	if err != nil {
		t.Fatalf("list internal/outbox: %v", err)
	}
	topics := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if !strings.HasPrefix(name.Name, "Topic") || i >= len(value.Values) {
						continue
					}
					call, ok := value.Values[i].(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						t.Fatalf("outbox.%s is not a topic[...](\"name\") call; read it some other way", name.Name)
					}
					lit, ok := call.Args[0].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("outbox.%s is not a string literal; read it some other way", name.Name)
					}
					topic, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("outbox.%s: %v", name.Name, err)
					}
					topics[name.Name] = topic
				}
			}
		}
	}
	return topics
}

// handled reports whether outboxStore already has a handler for topic, by asking
// for a second one: outbox refuses a duplicate registration by panicking.
func handled(t *testing.T, outboxStore *outbox.Store, topic string) (registered bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(string)
			if !ok || !strings.Contains(msg, "duplicate handler") {
				t.Fatalf("registering %q panicked for another reason: %v", topic, r)
			}
			registered = true
		}
	}()
	outboxStore.Handle(topic, func(context.Context, []byte) error { return nil })
	return false
}

// TestAMissingSellerIsAnnouncedAtStartup keeps the omission of the 消保法 §18
// disclosure from mail visible: with either value blank nothing else says so.
func TestAMissingSellerIsAnnouncedAtStartup(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]config{
		"both blank":    {},
		"contact blank": {Seller: "Shop"},
		"name blank":    {SellerContact: "a@b.example"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			buf := &bytes.Buffer{}
			warnSellerUnset(&cfg, slog.New(slog.NewTextHandler(buf, nil)))
			for _, want := range []string{"level=WARN", "GOEN_SELLER", "GOEN_SELLER_CONTACT"} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log %q lacks %q", buf.String(), want)
				}
			}
		})
	}

	buf := &bytes.Buffer{}
	warnSellerUnset(&config{Seller: "Shop", SellerContact: "a@b.example"}, slog.New(slog.NewTextHandler(buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("a configured seller logged %q", buf.String())
	}
}

// TestSellerVariablesAreDocumented holds the two names in the files a deployer
// reads, since the binary's reads are the only other place they appear.
func TestSellerVariablesAreDocumented(t *testing.T) {
	t.Parallel()

	for _, file := range []string{
		filepath.Join("..", "..", ".env.example"),
		filepath.Join("..", "..", "deploy", "demo", "manifest.env"),
	} {
		//nolint:gosec // G304: the two paths are fixed above
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, name := range []string{"GOEN_SELLER=", "GOEN_SELLER_CONTACT="} {
			if !strings.Contains(string(raw), name) {
				t.Errorf("%s does not document %s", file, name)
			}
		}
	}
}
