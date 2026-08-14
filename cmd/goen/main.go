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

// config is goen's entire runtime configuration. It comes from the
// environment only: there is no config file to fall out of step with a
// deployment.
type config struct {
	Addr        string
	DatabaseURL string
	LogLevel    slog.Level
	// SecureCookies is whether cookies may carry Secure and the __Host- prefix.
	// It defaults to ON, so a deployment that forgets to set it is safe rather
	// than silently issuing cookies any network can read. Development over
	// plain HTTP sets GOEN_INSECURE_COOKIES=1, because a Secure cookie is never
	// sent back over http:// and the cart would appear to lose itself.
	SecureCookies bool
	// AdminDatabaseURL is what the back office's pool connects with. It defaults
	// to the main URL, because in development one superuser can assume either
	// role; a deployment points it at admin_svc so the storefront and the back
	// office reach the database as different accounts.
	AdminDatabaseURL string
	// MaintenanceDatabaseURL is what the recommendation projection's pool
	// connects with. It is its own knob because the worker does
	// `SET ROLE maintenance`, and a pool opened from DatabaseURL does that on a
	// connection made as store_svc, which is not a member of that role.
	//
	// pgxpool connects LAZILY, so the site starts clean and the failure surfaces
	// only in the worker — permission denied at boot and again every fifteen
	// minutes, for as long as anybody leaves it running. A dev superuser may
	// assume any role, so a laptop is exactly where this goes unnoticed.
	MaintenanceDatabaseURL string
	// Stripe. An empty secret key is valid and means "this deployment does not
	// take money yet": goen browses and places orders, and the payment page
	// says so. A key WITHOUT a webhook secret is refused at startup, because it
	// would leave the endpoint that marks orders paid unauthenticated.
	StripeSecretKey     string
	StripeWebhookSecret string
	// The 加值中心. Empty means goen issues no 統一發票 and the order page says
	// so, exactly as an empty Stripe key leaves the payment page saying
	// 金流尚未啟用. HALF a configuration does not start: a merchant id cannot
	// sign a request without its keys, and finding that out at the first issue
	// is finding it out after an order was placed and money taken.
	//
	// ECPay publish staging credentials anybody may use (MerchantID 2000132),
	// which is what makes this a real integration rather than a fake issuer.
	// GOEN_ECPAY_BASE_URL defaults to staging; production is a URL change.
	ECPayMerchantID string
	ECPayHashKey    string
	ECPayHashIV     string
	ECPayBaseURL    string
	// Google sign-in. Empty offers no button and 404s the routes: password
	// authentication is complete on its own, so this adds convenience rather
	// than capability. BOTH or neither, for the reason the 加值中心 needs all
	// three — a client id that cannot exchange a code fails AFTER the customer
	// has been to Google and consented, which looks like goen losing their
	// account.
	GoogleClientID     string
	GoogleClientSecret string
	// SMTP. An empty address means mail is written to the log instead of sent,
	// which is what a development machine should do — visibly, not silently.
	SMTPAddr     string
	SMTPFrom     string
	SMTPUser     string
	SMTPPassword string
	// BaseURL is the origin Stripe sends the customer back to. It has no
	// default: guessing it from the listen address produces 127.0.0.1 URLs that
	// work in development and silently break the moment anything is deployed.
	BaseURL string
	// Seller and SellerContact are 消保法 §18 I item 1 — who is selling, and how
	// a consumer reaches them quickly. They go into the order confirmation,
	// because §18 II wants the disclosure in a form the consumer can STORE.
	//
	// No default, and blank omits the disclosure rather than printing an empty
	// seller: a shop's registered name and its 統編 are facts about that shop,
	// and a binary shipping somebody else's is worse than one shipping none.
	Seller        string
	SellerContact string
	// TOTPKey encrypts stored second-factor secrets.
	//
	// Empty disables enrolment, the same way an empty Stripe key disables
	// payment: the feature is off and the page says so, rather than half on
	// with secrets in the clear. A staff_totp_credentials row is a
	// password-equivalent, so this key is what stops a database dump alone
	// defeating the second factor.
	TOTPKey string
	// TrustedProxies is the CIDR list whose X-Forwarded-For goen will believe,
	// comma-separated. Empty — the default — means it believes nobody and keys
	// every rate limit on the connection's own address.
	//
	// That default is the safe one and it must stay the default: a header is set
	// by the CLIENT, so reading one on faith hands an attacker an unlimited
	// supply of rate-limit keys, which is strictly worse than having no limiter
	// because it looks like there is one. What the empty case costs is the
	// opposite failure — behind a TLS terminator every visitor collapses into a
	// single bucket, so one attacker can exhaust sign-in, /forgot, the newsletter
	// and order lookup for everybody at once. Only naming the proxies resolves
	// it, and only a deployment knows what they are.
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
		// No guess in a production posture. The default below contradicted this
		// field's own documentation — it said "it has no default: guessing it
		// from the listen address produces 127.0.0.1 URLs that work in
		// development and silently break the moment anything is deployed", and
		// then guessed exactly that. A container listening on 0.0.0.0 put
		// http://0.0.0.0:9700 into Stripe's success_url, every password-reset
		// link, every unsubscribe link and the sitemap.
		//
		// Development keeps the convenience because there the guess is right and
		// requiring it would be friction with no reader; anything with TLS in
		// front of it is refused below in [config.validate].
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
// a security feature silently off.
//
// SecureCookies is the production signal already in this config — it is false
// only under GOEN_INSECURE_COOKIES, which is the development opt-out — so a
// developer over plain HTTP is unaffected and gets a warning instead.
//
// Both of the settings it checks fail OPEN when unset, which is why they are
// checked here rather than described as "the same shape as Stripe" in a wiring
// comment. An empty TOTP key leaves the step-up function nil and RequireStaff
// skips it, so the whole back office falls back to a password with nothing
// anywhere saying so; a guessed BaseURL puts the container's own listen address
// into Stripe's success_url and into every link goen mails. Stripe itself
// refuses to start on a half-configuration, and this function is what holds the
// other two to the same line.
func (cfg *config) checkProductionPosture(log *slog.Logger) error {
	// The second factor fails CLOSED in a production posture, and this is the
	// line that makes that true rather than aspirational.
	//
	// An empty GOEN_TOTP_KEY leaves the step-up function nil, and RequireStaff
	// skips the check when it is nil — so without this the entire back office
	// falls back to a password alone, and silently: the one page that could say
	// so overwrites its own warning, and nothing reaches the log at startup.
	//
	// SecureCookies is the production signal already in this config — it is off
	// only under GOEN_INSECURE_COOKIES, which is the development opt-out — so a
	// developer running over plain HTTP is unaffected and gets the warning
	// instead. Anything that has TLS in front of it must set a key or not serve.
	if cfg.TOTPKey == "" {
		if cfg.SecureCookies {
			return errors.New("GOEN_TOTP_KEY is required: without it the back office " +
				"is reachable with a password alone. Set it, or set " +
				"GOEN_INSECURE_COOKIES=1 for local development")
		}
		log.Warn("GOEN_TOTP_KEY is not set; the back office has NO second factor",
			"set", "GOEN_TOTP_KEY")
	}

	// A guessed BaseURL is a development convenience and a production defect.
	// Every link goen mails and every URL it hands Stripe is built from it, so a
	// deployment that did not set one sends customers to the container's own
	// listen address. Same production signal as the key above.
	if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" {
		return errors.New("GOEN_BASE_URL is required: it is the origin in every " +
			"link goen mails and every URL it gives Stripe, and guessing it from " +
			"the listen address produces URLs that only work on this machine")
	}
	return nil
}

// trustedProxies is the CIDR set whose X-Forwarded-For goen will believe.
//
// A parse failure is FATAL rather than a warning, and that is the point of
// validating it here: a deployment that meant to name its load balancer and
// mistyped the CIDR would otherwise start happily and keep the collapsed
// single-bucket behaviour it is configuring its way out of — the failure being
// fixed, still in place, now believed fixed.
func (cfg *config) trustedProxies(log *slog.Logger) (*ratelimit.Proxies, error) {
	// A parse failure is FATAL rather than a warning, and that is the whole point
	// of validating it here: a deployment that meant to name its load balancer
	// and mistyped the CIDR would otherwise start happily and keep the collapsed
	// single-bucket behaviour it was configuring its way out of — the failure
	// being fixed, still in place, now believed fixed.
	proxies, err := ratelimit.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("GOEN_TRUSTED_PROXIES: %w", err)
	}
	if cfg.TrustedProxies == "" && cfg.SecureCookies {
		// Not fatal: a deployment with no proxy in front of it is a perfectly
		// good deployment, and goen cannot tell the two apart from in here.
		log.Warn("GOEN_TRUSTED_PROXIES is not set; if anything terminates TLS in "+
			"front of goen, every visitor shares one rate-limit bucket",
			"set", "GOEN_TRUSTED_PROXIES")
	}
	return proxies, nil
}

// newServer builds the HTTP server, with its timeouts and its outermost
// middleware. Split out of run() because that function had grown past its
// complexity budget, and this is the part of it that is pure assembly.
func newServer(
	cfg *config, log *slog.Logger, proxies *ratelimit.Proxies,
	pool, adminPool *pgxpool.Pool, gateway *payment.Gateway, refunder admin.Refunder,
	invoices *invoice.Gateway, googleSignIn *account.Google,
) *http.Server {
	return &http.Server{
		Addr: cfg.Addr,
		// Resolve is OUTERMOST, ahead of every ratelimit.Guard, because what it
		// does is decide which address the request came from — and a limiter that
		// keys on that has to run after the answer exists. With no trusted
		// proxies it returns the handler unwrapped, so the default costs nothing
		// at all, not even a context value.
		Handler: proxies.Resolve(newRouter(pool, adminPool, gateway, refunder, &RouterConfig{
			BaseURL: cfg.BaseURL, SecureCookies: cfg.SecureCookies, TOTPKey: cfg.TOTPKey,
			Invoices: invoices, Google: googleSignIn,
		}, log)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Default is 1 MiB. goen sends no large headers and receives none it
		// needs, so the smaller cap costs nothing and closes a cheap way to
		// hold a connection open.
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

// reachDatabase proves the pool works before anything else is built, and warns
// about a login role that could undo the privilege model.
//
// Split out of run() because that function had grown past its complexity budget.
func reachDatabase(ctx context.Context, pool *pgxpool.Pool, url string, log *slog.Logger) error {
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if pingErr := pool.Ping(pingCtx); pingErr != nil {
		return fmt.Errorf("reach database: %w", redactURL(pingErr, url))
	}

	// The connection guard (openPool) already refuses a session that stays
	// superuser after SET ROLE store. A superuser LOGIN role is weaker but
	// still unsafe — it can RESET ROLE back to full privilege — yet the dev
	// Makefile connects as the owning superuser on purpose, so warn rather than
	// refuse. Production points GOEN_DATABASE_URL at a non-superuser member of
	// store (store_svc) and this line stays quiet.
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
//
// It uses the same URL as the storefront by default: in development the owning
// superuser can assume either role, and a deployment points
// GOEN_ADMIN_DATABASE_URL at admin_svc so the two connect as different accounts.
//
// REACHED, not just opened, and that is the whole reason this is not two lines
// inline. pgxpool connects lazily, so a wrong admin DSN produced a clean start,
// a 200 from /readyz and a back office that 500ed on every page — the storefront
// pool was the only one anything asked about. Failing here is the right shape: a
// process that cannot do half its job should not be the one an orchestrator is
// told to send traffic to. health.NewHandler now asks the same question of both
// pools for the rest of the process's life.
func reachableAdminPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := openAdminPool(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open admin pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if pingErr := pool.Ping(pingCtx); pingErr != nil {
		pool.Close()
		// The URL carries the password; report the failure without it.
		return nil, fmt.Errorf("reach admin database: %w", redactURL(pingErr, url))
	}
	return pool, nil
}

// openInvoicing builds the 加值中心 gateway and says when there is none.
//
// Empty credentials issue nothing and the order page says so; HALF a
// configuration does not start, because a merchant id cannot sign a request
// without its keys — and finding that out at the first issue is finding it out
// after an order was placed and money taken. Exactly the shape a Stripe key
// without its webhook secret has.
func openInvoicing(cfg *config, log *slog.Logger) (*invoice.Gateway, error) {
	g, err := invoice.NewGateway(cfg.ECPayMerchantID, cfg.ECPayHashKey,
		cfg.ECPayHashIV, cfg.ECPayBaseURL)
	if err != nil {
		return nil, err
	}
	if !g.Enabled() {
		// i18n-exempt: a startup log line, read by an operator rather than a
		// visitor — the same category as the Stripe and TOTP warnings beside it.
		log.Warn("no 加值中心 configured; goen will issue no 統一發票",
			"set", "GOEN_ECPAY_MERCHANT_ID, GOEN_ECPAY_HASH_KEY and GOEN_ECPAY_HASH_IV")
	}
	return g, nil
}

// openProviders builds the outside services goen talks to and says when any is
// absent.
//
// Together, because they share one posture: a deployment with no key still
// serves the whole site and the page that would use it says so, while HALF a
// configuration does not start at all. A Stripe key without its webhook secret
// would take money over an unauthenticated endpoint; a 加值中心 merchant id
// without its keys cannot sign a request; a Google client id without its secret
// fails after the customer has already consented — and finding any of them out
// at the first use is finding it out too late.
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
//
// Absent credentials are an ordinary deployment: password authentication is
// complete, so Google adds convenience rather than capability and its absence
// changes nothing except that the button is gone.
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
		// The URL carries the password; report the failure without it.
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
	// The back office's own Stripe client. A separate one from the storefront's
	// gateway because it makes different calls with a different reason to fail:
	// approving a return pays money OUT.
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

	// Abandoned checkouts hold stock until something gives it back. The sweeper
	// is that something, and it is OWNED here rather than started and forgotten:
	// wg.Go plus the shutdown context means a stop waits for a release that is
	// mid-flight instead of cutting it between its two writes.
	var background sync.WaitGroup
	sweeper := cart.NewStore(pool)
	background.Go(func() { sweeper.SweepForever(ctx, log) })
	// A second worker on the same store, with its own interval. Returning an
	// expired hold to the shelf is urgent; pruning a month-old idempotency key is
	// not, and running the two on one ticker would give the cheap job the
	// expensive one's cadence.
	background.Go(func() { sweeper.SweepAttemptsForever(ctx, log) })

	// The outbox. Messages are written in the same transaction as the fact they
	// are about; this is what delivers them, and it is owned here for the same
	// reason the sweeper is — a shutdown must wait for a delivery in flight
	// rather than cut it between the send and the stamp.
	// Opened HERE and not inside startWorkers: a pool closed by that function's
	// own defer would be closed the moment it returns, which is before the
	// worker it belongs to has done anything.
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
		// stop() BEFORE returning, and that ordering is the whole fix.
		//
		// `defer stop()` is registered near the top and `defer background.Wait()`
		// far below, so LIFO runs Wait() FIRST — with the context still live,
		// because nothing had cancelled it on this path. Every worker sits in its
		// `select { case <-ctx.Done() }` and Wait() blocks: a port-in-use failure
		// produced a process that hung instead of exiting, so the crash-loop
		// signal an orchestrator depends on never arrived until it timed out and
		// killed the thing itself.
		//
		// Calling stop() here cancels the context first, the workers drain, Wait()
		// returns, and the process exits non-zero the moment it cannot serve.
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
//
// Two things beyond a bare pgxpool.New. The timeouts come from the database
// rules: a connection that cannot be reached should fail, not hang, and a
// pooled connection is recycled rather than kept forever. And every connection
// does SET ROLE store on acquisition, which is what makes the privilege
// model in the schema bite: the running binary operates as store — barred
// from writing stock, money or a ledger except through the SECURITY DEFINER
// functions — no matter which login role the deployment connects with. The
// migration tool connects separately, as the owner, and is unaffected.
func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, "store", 0)
}

// openAdminPool builds the pool the back office serves from.
//
// A SECOND pool rather than SET ROLE per request. Setting the role on a request
// would leave it set on a connection handed back to the pool, and the next
// storefront request would run as admin — a privilege escalation created by
// connection reuse, which is exactly the kind that survives review because
// nothing in the handler looks wrong.
func openAdminPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, "admin", 0)
}

// openMaintenancePool builds the pool background jobs run on.
//
// A THIRD pool for one function call, which is worth it for the same reason the
// second one is: the projection rebuild must be something a request cannot do,
// and the only way to say that is a role a request never holds. Granting
// refresh_copurchases to `store` instead would make it callable from any
// handler — 584 ms of work, on demand, by anyone.
//
// Two connections, because there is one ticker and a second is slack for a
// rebuild that outlives its interval.
func openMaintenancePool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	// maxConns is applied BEFORE the pool is built, and that is load-bearing
	// rather than tidiness. pgxpool.Pool.Config() returns a COPY
	// (`return p.config.Copy()`), so `pool.Config().MaxConns = 2` written after
	// pgxpool.NewWithConfig lands on a discarded object and the pool keeps the
	// default of max(4, numCPU) — leaving the two-connection reasoning above
	// describing nothing that is in force, with no symptom to notice.
	return openPoolAs(ctx, url, "maintenance", 2)
}

// openPoolAs builds a pool whose every connection assumes role.
//
// Beyond a bare pgxpool.New: the timeouts come from the database rules — a
// connection that cannot be reached should fail rather than hang, and a pooled
// connection is recycled rather than kept forever — and every connection does
// SET ROLE on acquisition, which is what makes the privilege model in the
// schema bite. The binary operates as that role no matter which login role the
// deployment connects with. The migration tool connects separately, as the
// owner, and is unaffected.
// maxConns of 0 keeps pgx's own default, which is what the storefront and the
// back office want; only the maintenance pool asks for a smaller one, and it has
// to ask HERE because Config() on a built pool hands back a copy.
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
		// The role name is this function's own argument, never anything a
		// request supplied, so it cannot be an injection point.
		if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
			return fmt.Errorf("assume %s role: %w", role, err)
		}
		// The privilege model is only real if the running session cannot ignore
		// it. A superuser bypasses every REVOKE, so if the session is still a
		// superuser after SET ROLE — meaning the role itself was granted
		// superuser — refuse to serve. This holds in development too, where the
		// login role is the owning superuser but SET ROLE drops it.
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

// redactURL removes the database URL from an error message. pgx includes the
// connection string in some failures, and that string holds the password.
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

// newSender picks how mail leaves the process.
//
// No SMTP address means the log, and it says so in every line it writes. A
// silent no-op would let a deployment run for a week before anybody noticed no
// confirmation had ever arrived.
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
			// Not fatal: mail goes to the log and the site still serves. A
			// malformed SMTP address should not stop a shop taking orders.
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
//
// A struct because the list had reached six, half of them pointers that a
// positional call site could not tell apart.
type workerDeps struct {
	pool *pgxpool.Pool
	// admin is the back office's pool, and the workers that use it are the
	// HOUSEKEEPING ones. A sweep is not a request, and the privileges it needs
	// are the ones the role that answers requests must not have.
	admin       *pgxpool.Pool
	maintenance *pgxpool.Pool
	cfg         *config
	log         *slog.Logger
	// run starts one worker. It is the errgroup's Go, passed in rather than the
	// group itself, because these workers return nothing and the group's other
	// methods are not this function's business.
	run func(func())
}

// startWorkers wires everything that runs on its own schedule.
//
// Extracted from run() because they are one group — each owns a ticker or a
// loop, each stops with ctx, and together they were half of run()'s branches.
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
	messages.Handle(outbox.TopicNewsletterIssue, func(ctx context.Context, payload []byte) error {
		var p email.NewsletterIssue
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendNewsletterIssue(ctx, &p)
	})
	messages.Handle(outbox.TopicRestocked, func(ctx context.Context, payload []byte) error {
		var p email.RestockNotice
		if decodeErr := outbox.Decode(payload, &p); decodeErr != nil {
			return decodeErr
		}
		return notifier.SendRestockNotice(ctx, &p)
	})
	d.run(func() { messages.Run(ctx) })
	// And the retention sweep. outbox_messages is the busiest write path in the
	// schema and nothing pruned it: every message goen ever sent stayed, with its
	// payload, which is where the mailed tokens live.
	d.run(func() { messages.SweepForever(ctx, d.log) })

	// Housekeeping. Neither changes an answer — expiry is enforced in the
	// queries that read a session, and an unreferenced upload is invisible —
	// but both tables grow without bound otherwise, and a session row holds the
	// user id it belonged to long after it stops meaning anything.
	d.run(func() { account.NewStore(d.pool).SweepSessionsForever(ctx, d.log) })
	// The media sweeper is on the ADMIN pool, and it was on the storefront one —
	// where `store` holds SELECT on media_objects and nothing else, so the DELETE
	// was refused every hour since the sweeper was written and NOTHING had ever
	// been reclaimed.
	//
	// It failed quietly because the refusal was logged at Warn under a comment
	// saying a failed delete is "a foreign key refusing, and that is the guard
	// working" — so a permission denial read as the guard working. Sweep returned
	// nil, reclaimed stayed 0, and SweepForever logs nothing when it reclaims
	// nothing. Images are bytes in PostgreSQL at up to 8 MiB each.
	//
	// admin rather than a grant to store, and the two tables get different
	// answers on purpose: deleting stored bytes is irreversible, so the role that
	// serves anonymous product pages must not be able to do it. Reclaiming an
	// upload nobody attached is the shop's own housekeeping.
	d.run(func() { media.NewStore(d.admin).SweepForever(ctx, d.log) })

	// The co-purchase projection, on its own pool as `maintenance`:
	// refresh_copurchases writes a table neither `store` nor `admin` may write,
	// and neither may CALL the function — which is what stops a request
	// rebuilding the recommendations it reads.
	d.run(func() { recommend.NewStore(d.maintenance, d.log).RefreshForever(ctx) })
}
