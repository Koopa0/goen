// Command goen serves the goen storefront.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/recommend"
)

func main() {
	if err := run(); err != nil {
		slog.Error("goen exited", "error", err)
		os.Exit(1)
	}
}

// config is goen's runtime configuration, from the environment only.
type config struct {
	Addr        string
	DatabaseURL string
	LogLevel    slog.Level
	// SecureCookies defaults to ON; development over plain HTTP opts out with
	// GOEN_INSECURE_COOKIES=1, because a Secure cookie never comes back over
	// http:// and the cart would appear to lose itself.
	SecureCookies    bool
	AdminDatabaseURL string
	// MaintenanceDatabaseURL is its own knob because a pool opened from
	// DatabaseURL does SET ROLE maintenance as store_svc, which is not a member
	// of that role — and pgxpool connects lazily, so it fails only in the worker.
	MaintenanceDatabaseURL string
	StripeSecretKey        string
	StripeWebhookSecret    string
	ECPayMerchantID        string
	ECPayHashKey           string
	ECPayHashIV            string
	ECPayBaseURL           string
	GoogleClientID         string
	GoogleClientSecret     string
	SMTPAddr               string
	SMTPFrom               string
	SMTPUser               string
	SMTPPassword           string
	BaseURL                string
	// Seller and SellerContact are Consumer Protection Act §18 I item 1, carried
	// into the order confirmation because §18 II wants a form the consumer can
	// store. Blank omits the disclosure rather than naming nobody.
	Seller        string
	SellerContact string
	TOTPKey       string
	// TrustedProxies is the CIDR list whose X-Forwarded-For goen will believe.
	// Empty is the default and must stay it: a header is set by the client, so
	// believing one hands an attacker an unlimited supply of rate-limit keys.
	TrustedProxies string
}

func loadConfig() (config, error) {
	url := os.Getenv("GOEN_DATABASE_URL")
	if url == "" {
		return config{}, errors.New("GOEN_DATABASE_URL is required")
	}

	level, err := parseLevel(envOr("GOEN_LOG_LEVEL", "info"))
	if err != nil {
		return config{}, err
	}

	return config{
		Addr:             envOr("GOEN_ADDR", "127.0.0.1:9700"),
		DatabaseURL:      url,
		LogLevel:         level,
		SecureCookies:    os.Getenv("GOEN_INSECURE_COOKIES") != "1",
		AdminDatabaseURL: envOr("GOEN_ADMIN_DATABASE_URL", url),

		MaintenanceDatabaseURL: envOr("GOEN_MAINTENANCE_DATABASE_URL", url),

		StripeSecretKey:     os.Getenv("GOEN_STRIPE_SECRET_KEY"),
		ECPayMerchantID:     os.Getenv("GOEN_ECPAY_MERCHANT_ID"),
		ECPayHashKey:        os.Getenv("GOEN_ECPAY_HASH_KEY"),
		ECPayHashIV:         os.Getenv("GOEN_ECPAY_HASH_IV"),
		ECPayBaseURL:        os.Getenv("GOEN_ECPAY_BASE_URL"),
		GoogleClientID:      os.Getenv("GOEN_GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("GOEN_GOOGLE_CLIENT_SECRET"),
		StripeWebhookSecret: os.Getenv("GOEN_STRIPE_WEBHOOK_SECRET"),
		// The guess is a development convenience; checkProductionPosture refuses
		// it wherever cookies are Secure.
		BaseURL: envOr("GOEN_BASE_URL", "http://"+envOr("GOEN_ADDR", "127.0.0.1:9700")),

		Seller:        os.Getenv("GOEN_SELLER"),
		SellerContact: os.Getenv("GOEN_SELLER_CONTACT"),

		TOTPKey:        os.Getenv("GOEN_TOTP_KEY"),
		TrustedProxies: os.Getenv("GOEN_TRUSTED_PROXIES"),
		SMTPAddr:       os.Getenv("GOEN_SMTP_ADDR"),
		SMTPFrom:       envOr("GOEN_SMTP_FROM", "goen <no-reply@goen.example>"),
		SMTPUser:       os.Getenv("GOEN_SMTP_USER"),
		SMTPPassword:   os.Getenv("GOEN_SMTP_PASSWORD"),
	}, nil
}

// checkProductionPosture refuses a configuration that would serve the site with
// a security feature silently off. SecureCookies is the production signal:
// it is false only under the development opt-out GOEN_INSECURE_COOKIES.
func (cfg *config) checkProductionPosture(log *slog.Logger) error {
	// An empty key leaves the step-up function nil and RequireStaff skips the
	// check when it is nil, so the back office falls back to a password alone.
	if cfg.TOTPKey == "" {
		if cfg.SecureCookies {
			return errors.New("GOEN_TOTP_KEY is required: without it the back office " +
				"is reachable with a password alone. Set it, or set " +
				"GOEN_INSECURE_COOKIES=1 for local development")
		}
		log.Warn("GOEN_TOTP_KEY is not set; the back office has NO second factor",
			"set", "GOEN_TOTP_KEY")
	}

	if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" {
		return errors.New("GOEN_BASE_URL is required: it is the origin in every " +
			"link goen mails and every URL it gives Stripe, and guessing it from " +
			"the listen address produces URLs that only work on this machine")
	}
	return nil
}

// trustedProxies is the CIDR set whose X-Forwarded-For goen will believe. A
// mistyped CIDR is fatal rather than a warning: it would otherwise keep the
// collapsed single-bucket behaviour it is configuring its way out of.
func (cfg *config) trustedProxies(log *slog.Logger) (*ratelimit.Proxies, error) {
	proxies, err := ratelimit.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("GOEN_TRUSTED_PROXIES: %w", err)
	}
	if cfg.TrustedProxies == "" && cfg.SecureCookies {
		log.Warn("GOEN_TRUSTED_PROXIES is not set; if anything terminates TLS in "+
			"front of goen, every visitor shares one rate-limit bucket",
			"set", "GOEN_TRUSTED_PROXIES")
	}
	return proxies, nil
}

// newServer builds the HTTP server, with its timeouts and its outermost
// middleware.
func newServer(
	cfg *config, log *slog.Logger, proxies *ratelimit.Proxies,
	pool, adminPool *pgxpool.Pool, gateway *payment.Gateway, refunder admin.Refunder,
	invoices *invoice.Gateway, googleSignIn *account.Google,
) *http.Server {
	return &http.Server{
		Addr: cfg.Addr,
		// Resolve decides which address a request came from, so it sits outside
		// every ratelimit.Guard that keys on the answer.
		Handler: proxies.Resolve(newRouter(pool, adminPool, gateway, refunder, &RouterConfig{
			BaseURL: cfg.BaseURL, SecureCookies: cfg.SecureCookies, TOTPKey: cfg.TOTPKey,
			Invoices: invoices, Google: googleSignIn,
		}, log)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

// reachDatabase proves the pool works before anything else is built, and warns
// about a login role that could undo the privilege model.
func reachDatabase(ctx context.Context, pool *pgxpool.Pool, url string, log *slog.Logger) error {
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if pingErr := pool.Ping(pingCtx); pingErr != nil {
		return fmt.Errorf("reach database: %w", redactURL(pingErr, url))
	}

	// A superuser login role can RESET ROLE back to full privilege. The dev
	// Makefile connects as the owning superuser on purpose, so warn rather than
	// refuse.
	var loginIsSuper bool
	if roleErr := pool.QueryRow(ctx,
		"SELECT rolsuper FROM pg_roles WHERE rolname = session_user",
	).Scan(&loginIsSuper); roleErr == nil && loginIsSuper {
		log.Warn("connected as a superuser login role; the privilege model can be " +
			"reset away with RESET ROLE — use a non-superuser member of store in production")
	}
	return nil
}

// reachableAdminPool opens the back office's pool and proves it answers.
// pgxpool connects lazily, so a wrong admin DSN otherwise gives a clean start,
// a 200 from /readyz and a back office that 500s on every page.
func reachableAdminPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := openAdminPool(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open admin pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if pingErr := pool.Ping(pingCtx); pingErr != nil {
		pool.Close()
		return nil, fmt.Errorf("reach admin database: %w", redactURL(pingErr, url))
	}
	return pool, nil
}

// openInvoicing builds the e-invoice provider gateway, or says there is none.
func openInvoicing(cfg *config, log *slog.Logger) (*invoice.Gateway, error) {
	g, err := invoice.NewGateway(cfg.ECPayMerchantID, cfg.ECPayHashKey,
		cfg.ECPayHashIV, cfg.ECPayBaseURL)
	if err != nil {
		return nil, err
	}
	if !g.Enabled() {
		// i18n-exempt: a startup log line, read by an operator rather than a visitor.
		log.Warn("no 加值中心 configured; goen will issue no 統一發票",
			"set", "GOEN_ECPAY_MERCHANT_ID, GOEN_ECPAY_HASH_KEY and GOEN_ECPAY_HASH_IV")
	}
	return g, nil
}

// openProviders builds the outside services goen talks to and says when any is
// absent. Each refuses to start on HALF a configuration.
func openProviders(cfg *config, log *slog.Logger) (
	payments *payment.Gateway, invoices *invoice.Gateway,
	googleSignIn *account.Google, err error,
) {
	if payments, err = payment.NewGateway(cfg.StripeSecretKey, cfg.StripeWebhookSecret, cfg.BaseURL); err != nil {
		return nil, nil, nil, err
	}
	if invoices, err = openInvoicing(cfg, log); err != nil {
		return nil, nil, nil, err
	}
	if googleSignIn, err = openGoogleSignIn(cfg, log); err != nil {
		return nil, nil, nil, err
	}
	return payments, invoices, googleSignIn, nil
}

// openGoogleSignIn builds the OAuth client and says when there is none.
func openGoogleSignIn(cfg *config, log *slog.Logger) (*account.Google, error) {
	g, err := account.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if !g.Enabled() {
		log.Info("google sign-in is not configured; customers sign in with a password",
			"set", "GOEN_GOOGLE_CLIENT_ID and GOEN_GOOGLE_CLIENT_SECRET")
	}
	return g, nil
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := openPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database pool: %w", redactURL(err, cfg.DatabaseURL))
	}
	defer pool.Close()

	if reachErr := reachDatabase(ctx, pool, cfg.DatabaseURL, log); reachErr != nil {
		return reachErr
	}

	adminPool, adminErr := reachableAdminPool(ctx, cfg.AdminDatabaseURL)
	if adminErr != nil {
		return adminErr
	}
	defer adminPool.Close()

	gateway, invoices, googleSignIn, err := openProviders(&cfg, log)
	if err != nil {
		return err
	}
	refunder := admin.NewRefunder(cfg.StripeSecretKey)

	if !gateway.Enabled() {
		log.Warn("stripe is not configured; the payment page will say so",
			"set", "GOEN_STRIPE_SECRET_KEY and GOEN_STRIPE_WEBHOOK_SECRET")
	}

	if postureErr := cfg.checkProductionPosture(log); postureErr != nil {
		return postureErr
	}

	proxies, proxyErr := cfg.trustedProxies(log)
	if proxyErr != nil {
		return proxyErr
	}

	srv := newServer(&cfg, log, proxies, pool, adminPool, gateway, refunder, invoices, googleSignIn)

	var background sync.WaitGroup
	sweeper := cart.NewStore(pool)
	background.Go(func() { sweeper.SweepForever(ctx, log) })
	background.Go(func() { sweeper.SweepAttemptsForever(ctx, log) })

	// Opened here and not inside startWorkers: a pool closed by that function's
	// own defer would be closed before the worker it belongs to has done
	// anything.
	maintenancePool, err := openMaintenancePool(ctx, cfg.MaintenanceDatabaseURL)
	if err != nil {
		return fmt.Errorf("open maintenance pool: %w", err)
	}
	defer maintenancePool.Close()

	startWorkers(ctx, workerDeps{
		pool: pool, admin: adminPool, maintenance: maintenancePool,
		cfg: &cfg, log: log, run: background.Go,
	})

	defer background.Wait()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("goen serving", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		// stop() before returning: LIFO runs the deferred background.Wait()
		// before the deferred stop(), so without this the workers never see
		// cancellation and a port-in-use failure hangs instead of exiting.
		stop()
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("goen shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server: %w", err)
	}
	return nil
}

// openPool builds the connection pool goen serves from.
func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, "store", 0)
}

// openAdminPool builds the pool the back office serves from. A second pool
// rather than SET ROLE per request, which would leave the role set on a pooled
// connection and run the next storefront request as admin.
func openAdminPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, "admin", 0)
}

// openMaintenancePool builds the pool background jobs run on, as a role no
// request ever holds.
func openMaintenancePool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, "maintenance", 2)
}

// openPoolAs builds a pool whose every connection assumes role. maxConns of 0
// keeps pgx's default, and a caller wanting fewer has to ask here because
// Config() on a built pool hands back a copy.
func openPoolAs(ctx context.Context, url, role string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			return fmt.Errorf("assume %s role: %w", role, err)
		}
		// A superuser bypasses every REVOKE, so a session still superuser after
		// SET ROLE is not bound by the schema's privilege model at all.
		var superAsApp bool
		if err := conn.QueryRow(ctx,
			"SELECT current_setting('is_superuser')::boolean",
		).Scan(&superAsApp); err != nil {
			return fmt.Errorf("check privilege boundary: %w", err)
		}
		if superAsApp {
			return fmt.Errorf("refusing to serve: the session is a superuser after "+
				"SET ROLE %s, so the schema's write REVOKEs do not bind it", role)
		}
		return nil
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("parse GOEN_LOG_LEVEL %q: %w", s, err)
	}
	return level, nil
}

// redactURL removes the database URL from an error message: pgx includes the
// connection string in some failures, and it holds the password.
func redactURL(err error, url string) error {
	if url == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), url, "[database url]")
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

// newSender picks how mail leaves the process; no SMTP address means the log.
func newSender(cfg *config, log *slog.Logger) email.Sender {
	if cfg.SMTPAddr == "" {
		log.Warn("no SMTP configured; email will be written to the log",
			"set", "GOEN_SMTP_ADDR")
		return email.LogSender{Log: log}
	}
	s := email.SMTPSender{Addr: cfg.SMTPAddr, From: cfg.SMTPFrom}
	if cfg.SMTPUser != "" {
		host, _, err := net.SplitHostPort(cfg.SMTPAddr)
		if err != nil {
			log.Error("GOEN_SMTP_ADDR is not host:port; falling back to the log",
				"addr", cfg.SMTPAddr, "error", err)
			return email.LogSender{Log: log}
		}
		s.Auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)
		s.TLSName = host
	}
	return s
}

// workerDeps is what the background workers need.
type workerDeps struct {
	pool        *pgxpool.Pool
	admin       *pgxpool.Pool
	maintenance *pgxpool.Pool
	cfg         *config
	log         *slog.Logger
	// run starts one worker.
	run func(func())
}

// startWorkers wires everything that runs on its own schedule.
func startWorkers(ctx context.Context, d workerDeps) {
	messages := outbox.NewStore(d.pool, d.log)
	notifier := email.Notifier{
		Sender: newSender(d.cfg, d.log), BaseURL: d.cfg.BaseURL,
		Seller: d.cfg.Seller, SellerContact: d.cfg.SellerContact,
	}
	messages.Handle(outbox.TopicOrderPlaced, func(ctx context.Context, payload []byte) error {
		var p email.OrderPlaced
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendOrderPlaced(ctx, &p)
	})
	messages.Handle(outbox.TopicPasswordReset, func(ctx context.Context, payload []byte) error {
		var p email.PasswordReset
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendPasswordReset(ctx, &p)
	})
	messages.Handle(outbox.TopicOrderPaid, func(ctx context.Context, payload []byte) error {
		var p email.OrderPaid
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendOrderPaid(ctx, &p)
	})
	messages.Handle(outbox.TopicOrderShipped, func(ctx context.Context, payload []byte) error {
		var p email.OrderShipped
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendOrderShipped(ctx, &p)
	})
	messages.Handle(outbox.TopicNewsletterConfirm, func(ctx context.Context, payload []byte) error {
		var p email.NewsletterConfirm
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendNewsletterConfirm(ctx, &p)
	})
	messages.Handle(outbox.TopicNewsletterWelcome, func(ctx context.Context, payload []byte) error {
		var p email.NewsletterWelcome
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendNewsletterWelcome(ctx, &p)
	})
	messages.Handle(outbox.TopicEmailVerify, func(ctx context.Context, payload []byte) error {
		var p email.EmailVerify
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendEmailVerify(ctx, &p)
	})
	messages.Handle(outbox.TopicNewsletterIssue,
		newsletterIssueHandler(newsletter.NewStore(d.pool), notifier))
	messages.Handle(outbox.TopicRestocked, func(ctx context.Context, payload []byte) error {
		var p email.RestockNotice
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendRestockNotice(ctx, &p)
	})
	d.run(func() { messages.Run(ctx) })
	d.run(func() { messages.SweepForever(ctx, d.log) })

	d.run(func() { account.NewStore(d.pool).SweepSessionsForever(ctx, d.log) })
	// The media sweeper runs on the ADMIN pool: `store` holds SELECT on
	// media_objects and nothing else, so from there the DELETE is refused every
	// hour, quietly, and nothing is ever reclaimed.
	d.run(func() { media.NewStore(d.admin).SweepForever(ctx, d.log) })

	d.run(func() { recommend.NewStore(d.maintenance, d.log).RefreshForever(ctx) })
}

// newsletterIssueHandler delivers one copy of an issue, and asks at DELIVERY
// whether the address still wants it.
//
// The send freezes one outbox row per subscriber and the queue drains at bulk
// priority behind every transactional message — minutes to hours for a real
// list — so an unsubscribe committing anywhere in that window used to have its
// copy delivered anyway. The producer and the handler were each individually
// correct and disagreed about WHEN consent is true, which is the shape every
// guard here is blind to.
func newsletterIssueHandler(subscribers *newsletter.Store, notifier email.Notifier) func(context.Context, []byte) error {
	return func(ctx context.Context, payload []byte) error {
		var p email.NewsletterIssue
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		wanted, err := subscribers.StillSubscribed(ctx, p.Email)
		if err != nil {
			return err
		}
		if !wanted {
			// Nothing left to do rather than a failure: sending is what must not
			// happen, and rescheduling would retry exactly that.
			return nil
		}
		return notifier.SendNewsletterIssue(ctx, &p)
	}
}
