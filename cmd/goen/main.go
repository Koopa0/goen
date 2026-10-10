// Command goen serves the goen storefront.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/recommend"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/web"
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
	// of that role.
	MaintenanceDatabaseURL string
	StripeAPIKey           string
	StripeWebhookSecret    string
	ECPayMerchantID        string
	ECPayHashKey           string
	ECPayHashIV            string
	ECPayBaseURL           string
	// ECPayLogistics is the 物流 contract this merchant holds, c2c or b2c, and
	// unset is the whole feature off: the checkout then offers no pickup.
	ECPayLogistics string
	// ECPayLogisticsBaseURL defaults to the staging map.
	ECPayLogisticsBaseURL string
	GoogleClientID        string
	GoogleClientSecret    string
	DemoAccountEmail      string
	DemoAccountPassword   string
	SMTPAddr              string
	SMTPFrom              string
	SMTPUser              string
	SMTPPassword          string
	BaseURL               string
	// Seller and SellerContact are Consumer Protection Act §18 I item 1, carried
	// into the order confirmation because §18 II wants a form the consumer can
	// store. Blank omits the disclosure rather than naming nobody.
	Seller        string
	SellerContact string
	TOTPKey       string
	// totpKey is parsed key material, kept distinct from the raw environment
	// string so nothing downstream decides the key format a second time.
	totpKey []byte
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

		StripeAPIKey:    envOr("GOEN_STRIPE_API_KEY", os.Getenv("GOEN_STRIPE_SECRET_KEY")),
		ECPayMerchantID: os.Getenv("GOEN_ECPAY_MERCHANT_ID"),
		ECPayHashKey:    os.Getenv("GOEN_ECPAY_HASH_KEY"),
		ECPayHashIV:     os.Getenv("GOEN_ECPAY_HASH_IV"),
		ECPayBaseURL:    os.Getenv("GOEN_ECPAY_BASE_URL"),
		ECPayLogistics:  os.Getenv("GOEN_ECPAY_LOGISTICS"),

		ECPayLogisticsBaseURL: os.Getenv("GOEN_ECPAY_LOGISTICS_BASE_URL"),
		GoogleClientID:        os.Getenv("GOEN_GOOGLE_CLIENT_ID"),
		GoogleClientSecret:    os.Getenv("GOEN_GOOGLE_CLIENT_SECRET"),
		DemoAccountEmail:      os.Getenv("GOEN_DEMO_ACCOUNT_EMAIL"),
		DemoAccountPassword:   os.Getenv("GOEN_DEMO_ACCOUNT_PASSWORD"),
		StripeWebhookSecret:   os.Getenv("GOEN_STRIPE_WEBHOOK_SECRET"),
		// The guess is a development convenience; prepareRuntimePosture refuses
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

// prepareRuntimePosture refuses a configuration that would serve the site with
// a security feature silently off, or a subsystem that reports success without
// doing its work, and prepares the parsed TOTP key and canonical origin.
// SecureCookies selects secure transport and account requirements. The provider
// environment follows the Stripe key, not the cookies; see prepareProviderPosture.
func (cfg *config) prepareRuntimePosture(log *slog.Logger) error {
	// Key shape is a fact, not a production-only preference: accepting a weak
	// passphrase in development would create credentials production cannot
	// safely read.
	key, keyErr := twofactor.ParseKey(cfg.TOTPKey)
	if keyErr != nil {
		return fmt.Errorf("GOEN_TOTP_KEY: %w", keyErr)
	}
	cfg.totpKey = key
	// An empty key leaves the step-up function nil and RequireStaff skips the
	// check when it is nil, so the back office falls back to a password alone.
	if len(key) == 0 {
		if cfg.SecureCookies {
			return errors.New("GOEN_TOTP_KEY is required: without it the back office " +
				"is reachable with a password alone. Set it, or set " +
				"GOEN_INSECURE_COOKIES=1 for local development")
		}
		log.Warn("GOEN_TOTP_KEY is not set; the back office has NO second factor",
			"set", "GOEN_TOTP_KEY")
	}

	// An empty address leaves newNotifier on email.LogSender, which returns nil —
	// and nil is what the outbox reads as delivered. Password resets, receipts
	// and dispatch notices would therefore be stamped sent and go nowhere,
	// invisible to /admin/health because it asks only about undelivered rows.
	if cfg.SMTPAddr == "" {
		if cfg.SecureCookies {
			return errors.New("GOEN_SMTP_ADDR is required: without it mail is written " +
				"to the log and marked delivered, so password resets and order mail are " +
				"lost with nothing to show for it. Set it, or set " +
				"GOEN_INSECURE_COOKIES=1 for local development")
		}
		log.Warn("GOEN_SMTP_ADDR is not set; mail is logged, NOT sent, and marked delivered",
			"set", "GOEN_SMTP_ADDR")
	}

	if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" {
		return errors.New("GOEN_BASE_URL is required: it is the origin in every " +
			"link goen mails and every URL it gives Stripe, and guessing it from " +
			"the listen address produces URLs that only work on this machine")
	}
	origin, scheme, ok := web.SiteOrigin(cfg.BaseURL)
	if !ok && os.Getenv("GOEN_BASE_URL") == "" {
		return fmt.Errorf("GOEN_BASE_URL is unset and %q, guessed from GOEN_ADDR, is not an "+
			"origin: a listen address with no host or an unspecified one names no machine "+
			"a link can reach. Set GOEN_BASE_URL, or give GOEN_ADDR a host", cfg.BaseURL)
	}
	if !ok {
		return fmt.Errorf("GOEN_BASE_URL %q is not an origin: it must be scheme://host "+
			"with no path, query or credentials, because goen concatenates paths onto "+
			"it to build every link it mails", cfg.BaseURL)
	}
	if cfg.SecureCookies && scheme != "https" {
		return fmt.Errorf("GOEN_BASE_URL %q is plain HTTP while cookies are Secure: "+
			"every password-reset and address-verification link goen mails would carry "+
			"a single-use token in cleartext. Use https, or set "+
			"GOEN_INSECURE_COOKIES=1 for local development", cfg.BaseURL)
	}
	cfg.BaseURL = origin
	return cfg.prepareProviderPosture()
}

// trustedProxies is the CIDR set whose X-Forwarded-For goen will believe. A
// mistyped CIDR is fatal rather than a warning: it would otherwise keep the
// collapsed single-bucket behaviour it is configuring its way out of. So is a
// set no peer of the listener can be in, which configures nothing and starts
// looking configured.
func (cfg *config) trustedProxies(log *slog.Logger) (*ratelimit.Proxies, error) {
	proxies, err := ratelimit.ParseProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("GOEN_TRUSTED_PROXIES: %w", err)
	}
	if cfg.TrustedProxies != "" {
		if peers := listenerPeers(cfg.Addr); peers != nil && !slices.ContainsFunc(peers, proxies.Overlaps) {
			return nil, fmt.Errorf("GOEN_TRUSTED_PROXIES %q trusts no address that can connect "+
				"to GOEN_ADDR %s: only this host's loopback reaches that listener, so goen would "+
				"never read X-Forwarded-For and every visitor would share one rate-limit bucket. "+
				"Trust the proxy's own address: 127.0.0.1,::1 for a proxy on this host",
				cfg.TrustedProxies, cfg.Addr)
		}
	}
	if cfg.TrustedProxies == "" && cfg.SecureCookies {
		log.Warn("GOEN_TRUSTED_PROXIES is not set; if anything terminates TLS in "+
			"front of goen, every visitor shares one rate-limit bucket",
			"set", "GOEN_TRUSTED_PROXIES")
	}
	return proxies, nil
}

// The loopback networks. A connection to a loopback listener can only come
// from this host, and it arrives from its own family's loopback address.
var (
	loopbackV4 = netip.MustParsePrefix("127.0.0.0/8")
	loopbackV6 = netip.MustParsePrefix("::1/128")
)

// listenerPeers is where every connection to a listener on addr comes from,
// when that can be known: only for a loopback listener. nil is a listener any
// address might reach, such as ":9700", a wildcard, a host name or an
// interface address, where no trusted set can be ruled out.
func listenerPeers(addr string) []netip.Prefix {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		// net.Listen binds the first IPv4 address a host name resolves to, and
		// localhost resolves to 127.0.0.1, so a listener there is never reached
		// from ::1, even where ::1 is localhost too.
		return []netip.Prefix{loopbackV4}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return nil
	}
	if ip.Unmap().Is4() {
		return []netip.Prefix{loopbackV4}
	}
	return []netip.Prefix{loopbackV6}
}

func newServer(cfg *config, routes *RouterConfig, proxies *ratelimit.Proxies, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr: cfg.Addr,
		// Resolve decides which address a request came from, so it sits outside
		// every ratelimit.Guard that keys on the answer.
		Handler:           proxies.Resolve(newRouter(routes, log)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

// servingPool opens the storefront pool, proves it answers, and puts the demo
// account right before anything is served from it.
func servingPool(ctx context.Context, url string, demo account.DemoAccount, secureCookies bool, log *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := openPool(ctx, url, log)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", redactURL(err, url))
	}
	if err := reachDatabase(ctx, pool, url, storeRole, secureCookies, log); err != nil {
		pool.Close()
		return nil, err
	}
	if err := account.NewStore(pool).EnsureDemoAccount(ctx, demo); err != nil {
		pool.Close()
		return nil, fmt.Errorf("GOEN_DEMO_ACCOUNT_EMAIL: %w", err)
	}
	return pool, nil
}

type databaseRole string

const (
	storeRole       databaseRole = "store"
	adminRole       databaseRole = "admin"
	maintenanceRole databaseRole = "maintenance"
)

type databaseLoginPrivileges struct {
	superuser   bool
	ownerMember bool
}

func reachDatabase(ctx context.Context, pool *pgxpool.Pool, url string, role databaseRole, secureCookies bool, log *slog.Logger) error {
	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if pingErr := pool.Ping(pingCtx); pingErr != nil {
		return fmt.Errorf("reach %s database: %w", role, redactURL(pingErr, url))
	}

	// SET ROLE changes current_user, but session_user can still RESET ROLE or
	// assume the schema owner and escape the application's write restrictions.
	var privileges databaseLoginPrivileges
	if roleErr := pool.QueryRow(pingCtx, `
		SELECT r.rolsuper, pg_catalog.pg_has_role(session_user, c.relowner, 'USAGE')
		FROM pg_catalog.pg_roles r
		CROSS JOIN pg_catalog.pg_class c
		WHERE r.rolname = session_user AND c.oid = 'public.orders'::regclass
	`).Scan(&privileges.superuser, &privileges.ownerMember); roleErr != nil {
		return fmt.Errorf("check %s database login privileges: %w", role, redactURL(roleErr, url))
	}
	return checkDatabaseLogin(role, secureCookies, privileges, log)
}

func checkDatabaseLogin(role databaseRole, secureCookies bool, privileges databaseLoginPrivileges, log *slog.Logger) error {
	if !privileges.superuser && !privileges.ownerMember {
		return nil
	}
	message := fmt.Sprintf("%s database login can undo the privilege model; connect as a non-superuser member of %s", role, role)
	if secureCookies {
		return fmt.Errorf("refusing to start: %s", message)
	}
	log.Warn(message)
	return nil
}

// reachableAdminPool opens the back office's pool and proves it answers.
// pgxpool connects lazily, so a wrong admin DSN otherwise gives a clean start,
// a 200 from /readyz and a back office that 500s on every page.
func reachableAdminPool(ctx context.Context, url string, secureCookies bool, log *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := openAdminPool(ctx, url, log)
	if err != nil {
		return nil, fmt.Errorf("open admin pool: %w", err)
	}

	if err := reachDatabase(ctx, pool, url, adminRole, secureCookies, log); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

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

func openStoreMap(cfg *config, log *slog.Logger) (*cart.StoreMap, error) {
	m, err := cart.NewStoreMap(cfg.ECPayMerchantID, cfg.ECPayLogistics,
		cfg.ECPayLogisticsBaseURL, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if !m.Enabled() {
		// i18n-exempt: a startup log line, read by an operator rather than a visitor.
		log.Info("no 超商 store map configured; checkout offers no store pickup",
			"set", "GOEN_ECPAY_LOGISTICS")
	}
	return m, nil
}

// openProviders builds the outside services goen talks to and says when any is
// absent. Each refuses to start on HALF a configuration.
func openProviders(cfg *config, log *slog.Logger) (
	payments *payment.Gateway, invoices *invoice.Gateway,
	googleSignIn *account.Google, storeMap *cart.StoreMap, err error,
) {
	if payments, err = payment.NewGateway(cfg.StripeAPIKey, cfg.StripeWebhookSecret, cfg.BaseURL); err != nil {
		return nil, nil, nil, nil, err
	}
	if invoices, err = openInvoicing(cfg, log); err != nil {
		return nil, nil, nil, nil, err
	}
	if googleSignIn, err = openGoogleSignIn(cfg, log); err != nil {
		return nil, nil, nil, nil, err
	}
	if storeMap, err = openStoreMap(cfg, log); err != nil {
		return nil, nil, nil, nil, err
	}
	if !payments.Enabled() {
		log.Warn("stripe is not configured; the payment page will say so",
			"set", "GOEN_STRIPE_API_KEY and GOEN_STRIPE_WEBHOOK_SECRET")
	}
	warnSellerUnset(cfg, log)
	return payments, invoices, googleSignIn, storeMap, nil
}

// warnSellerUnset says when the confirmation mail will carry no seller
// disclosure: the mail omits it unless both values are set, and nothing else
// reports the omission.
func warnSellerUnset(cfg *config, log *slog.Logger) {
	if cfg.Seller != "" && cfg.SellerContact != "" {
		return
	}
	// i18n-exempt: a startup log line, read by an operator rather than a visitor.
	log.Warn("seller is not configured; order mail will carry no 消保法 §18 disclosure",
		"set", "GOEN_SELLER and GOEN_SELLER_CONTACT")
}

// demoAccount reads the account a public demonstration shares with every
// visitor, and says when there is one: its password is on the sign-in page.
func (cfg *config) demoAccount(log *slog.Logger) (account.DemoAccount, error) {
	d, err := account.NewDemoAccount(cfg.DemoAccountEmail, cfg.DemoAccountPassword)
	if err != nil {
		return account.DemoAccount{}, fmt.Errorf("GOEN_DEMO_ACCOUNT_EMAIL and GOEN_DEMO_ACCOUNT_PASSWORD: %w", err)
	}
	if d.Enabled() {
		log.Info("a shared demo account is offered on the sign-in page, with its password",
			"unset", "GOEN_DEMO_ACCOUNT_EMAIL and GOEN_DEMO_ACCOUNT_PASSWORD")
	}
	return d, nil
}

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
	cfg, configErr := loadConfig()
	if configErr != nil {
		return configErr
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if postureErr := cfg.prepareRuntimePosture(log); postureErr != nil {
		return postureErr
	}
	notifier, notifierErr := newNotifier(&cfg, log)
	if notifierErr != nil {
		return notifierErr
	}
	proxies, proxyErr := cfg.trustedProxies(log)
	if proxyErr != nil {
		return proxyErr
	}
	gateway, invoices, googleSignIn, storeMap, providerErr := openProviders(&cfg, log)
	if providerErr != nil {
		return providerErr
	}
	demoAccount, demoErr := cfg.demoAccount(log)
	if demoErr != nil {
		return demoErr
	}
	refunder := refunds.NewRefunder(cfg.StripeAPIKey)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, poolErr := servingPool(ctx, cfg.DatabaseURL, demoAccount, cfg.SecureCookies, log)
	if poolErr != nil {
		return poolErr
	}
	defer pool.Close()

	adminPool, adminErr := reachableAdminPool(ctx, cfg.AdminDatabaseURL, cfg.SecureCookies, log)
	if adminErr != nil {
		return adminErr
	}
	defer adminPool.Close()

	// Opened here and not inside startWorkers: a pool closed by that function's
	// own defer would be closed before the worker it belongs to has done
	// anything.
	maintenancePool, maintenanceErr := openMaintenancePool(ctx, cfg.MaintenanceDatabaseURL, cfg.SecureCookies, log)
	if maintenanceErr != nil {
		return fmt.Errorf("open maintenance pool: %w", maintenanceErr)
	}
	defer maintenancePool.Close()

	srv := newServer(&cfg, &RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: pool, Payments: gateway, BaseURL: cfg.BaseURL, SecureCookies: cfg.SecureCookies,
			Invoices: invoices, Google: googleSignIn, StoreMap: storeMap, DemoAccount: demoAccount,
		},
		BackOffice: BackOfficeConfig{
			AdminPool: adminPool, MaintenancePool: maintenancePool, Payments: gateway, Refunder: refunder,
			SecureCookies: cfg.SecureCookies, TOTPKey: cfg.totpKey, Invoices: invoices, StoreMap: storeMap,
		},
	}, proxies, log)

	// Nothing above starts a goroutine: a return between a worker and the Wait
	// that owns it would leave it running on a pool that is closing. Registered
	// after every Close so LIFO drains the workers first.
	var background sync.WaitGroup
	startWorkers(ctx, workerDeps{
		pool: pool, admin: adminPool, maintenance: maintenancePool,
		log: log, notifier: notifier, invoices: invoices, run: background.Go,
	})
	defer background.Wait()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("goen serving", "addr", cfg.Addr)
		if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serveErr <- listenErr
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
	return shutdown(srv, shutdownGrace)
}

// shutdownGrace is how long a request in flight at SIGTERM may take to finish.
const shutdownGrace = 15 * time.Second

// shutdown stops srv, giving the requests in flight grace to finish and then
// closing whatever is still open. Shutdown alone leaves those connections open
// with their requests running, and the pool closes deferred in run wait for
// every connection a request still holds; closing the connections is what
// cancels those requests' contexts.
func shutdown(srv *http.Server, grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}
	if closeErr := srv.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return fmt.Errorf("shut down server: %w", err)
}

// Pool size, request budget and statement bound per role. statement_timeout
// ends a slow query after a connection is acquired; storeRequestBudget ends
// pool acquisition and every store-pool round trip before http.Server's
// WriteTimeout, which does not cancel r.Context().
const (
	// pgx would otherwise take max(4, NumCPU), which is four on a small
	// container serving every visitor plus the background workers that share
	// this pool. 25 + 10 + 2 stays well inside PostgreSQL's default
	// max_connections of 100.
	storeMaxConns = 25
	// Hundreds of times the slowest measured storefront read, and under
	// WriteTimeout so a wedged query ends while the client is still there. The
	// retention sweeps that share this pool are logged and retried on the next
	// tick if their DELETE ever exceeds it.
	storeStatementTimeout = 15 * time.Second
	// Five seconds under WriteTimeout: enough headroom to render a failure after
	// a saturated pool wait plus a statement that runs the full store bound.
	storeRequestBudget = 25 * time.Second

	adminMaxConns = 10
	// /admin/reports scans order history over a 90-day window and grows with
	// the shop; a back office has one reader who can wait.
	adminStatementTimeout = 30 * time.Second

	maintenanceMaxConns = 2
	// refresh_copurchases is measured at 584 ms over the whole order history
	// and is meant to be slow — that is what its own role and pool are for.
	// This ends only a rebuild that has wedged, well inside its refresh
	// interval.
	maintenanceStatementTimeout = 5 * time.Minute
)

func openPool(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, log, storeRole, storeMaxConns, storeStatementTimeout)
}

// openAdminPool builds the pool the back office serves from. A second pool
// rather than SET ROLE per request, which would leave the role set on a pooled
// connection and run the next storefront request as admin.
func openAdminPool(ctx context.Context, url string, log *slog.Logger) (*pgxpool.Pool, error) {
	return openPoolAs(ctx, url, log, adminRole, adminMaxConns, adminStatementTimeout)
}

// openMaintenancePool builds and reaches the pool background jobs run on, as a
// role no request ever holds. pgxpool.NewWithConfig is lazy: Ping belongs here
// so a wrong independent DSN or a login that cannot SET ROLE maintenance stops
// startup rather than failing only inside an unattended worker.
func openMaintenancePool(ctx context.Context, url string, secureCookies bool, log *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := openPoolAs(ctx, url, log, maintenanceRole, maintenanceMaxConns, maintenanceStatementTimeout)
	if err != nil {
		return nil, redactURL(err, url)
	}

	if err := reachDatabase(ctx, pool, url, maintenanceRole, secureCookies, log); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// openPoolAs builds a pool whose every connection assumes role, holds at most
// maxConns of them, and runs no statement longer than statementTimeout. The
// limits are set here because Config() on a built pool hands back a copy.
func openPoolAs(
	ctx context.Context, url string, log *slog.Logger, role databaseRole,
	maxConns int32, statementTimeout time.Duration,
) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", redactURL(err, url))
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.Tracer = newSlowQueryTracer(log, string(role))
	// A bare number is milliseconds to PostgreSQL, and the startup packet is
	// what makes it a property of the connection rather than of a caller.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] =
		strconv.FormatInt(statementTimeout.Milliseconds(), 10)
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, roleErr := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{string(role)}.Sanitize()); roleErr != nil {
			return fmt.Errorf("assume %s role: %w", role, roleErr)
		}
		// A superuser bypasses every REVOKE, so a session still superuser after
		// SET ROLE is not bound by the schema's privilege model at all.
		var superAsApp bool
		if queryErr := conn.QueryRow(ctx,
			"SELECT current_setting('is_superuser')::boolean",
		).Scan(&superAsApp); queryErr != nil {
			return fmt.Errorf("check privilege boundary: %w", queryErr)
		}
		if superAsApp {
			return fmt.Errorf("refusing to serve: the session is a superuser after "+
				"SET ROLE %s, so the schema's write REVOKEs do not bind it", role)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, redactURL(err, url)
	}
	return pool, nil
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

// newSender chooses how mail leaves the process. A configured relay must never
// fall back to the log sender, whose nil return means "delivered".
func newSender(cfg *config, log *slog.Logger) (email.Sender, error) {
	if cfg.SMTPAddr == "" {
		return email.LogSender{Log: log}, nil
	}
	s := email.SMTPSender{Addr: cfg.SMTPAddr, From: cfg.SMTPFrom}
	if cfg.SMTPUser != "" {
		host, _, err := net.SplitHostPort(cfg.SMTPAddr)
		if err != nil {
			return nil, fmt.Errorf("GOEN_SMTP_ADDR %q is not host:port: %w", cfg.SMTPAddr, err)
		}
		s.Auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)
		s.TLSName = host
	}
	return s, nil
}

// newNotifier prepares mail delivery after prepareRuntimePosture has admitted
// the development-only log sender.
func newNotifier(cfg *config, log *slog.Logger) (email.Notifier, error) {
	sender, err := newSender(cfg, log)
	if err != nil {
		return email.Notifier{}, err
	}
	return email.New(sender, cfg.BaseURL, cfg.Seller, cfg.SellerContact), nil
}

type workerDeps struct {
	pool        *pgxpool.Pool
	admin       *pgxpool.Pool
	maintenance *pgxpool.Pool
	log         *slog.Logger
	notifier    email.Notifier
	invoices    *invoice.Gateway
	run         func(func())
}

func startWorkers(ctx context.Context, d workerDeps) {
	outboxStore := newOutboxStore(d)
	d.run(func() { outboxStore.Run(ctx) })
	d.run(func() { outboxStore.SweepForever(ctx, d.log) })

	holds := cart.NewStore(d.pool)
	d.run(func() { holds.SweepForever(ctx, d.log) })
	d.run(func() { holds.SweepAttemptsForever(ctx, d.log) })
	d.run(func() { orderaccess.NewStore(d.pool, false).SweepForever(ctx, d.log) })
	d.run(func() { holds.SweepDraftsForever(ctx, d.log) })

	d.run(func() { account.NewStore(d.pool).SweepSessionsForever(ctx, d.log) })
	// The media sweeper runs on the ADMIN pool: `store` holds SELECT on
	// media_objects and nothing else, so from there the DELETE is refused every
	// hour, quietly, and nothing is ever reclaimed.
	d.run(func() { media.NewStore(d.admin).SweepForever(ctx, d.log) })
	if d.invoices != nil && d.invoices.Enabled() {
		d.run(func() { invoice.NewStore(d.admin, d.invoices).ReconcileForever(ctx, d.log) })
	}

	d.run(func() { recommend.NewStore(d.maintenance, d.log).RefreshForever(ctx) })
}

// newOutboxStore is the outbox with a handler for each topic goen enqueues. A
// message whose topic has no handler is rescheduled for ever and fails nothing,
// so a missing line here is mail that silently never leaves.
func newOutboxStore(d workerDeps) *outbox.Store {
	outboxStore := outbox.NewStore(d.pool, d.log)
	outboxStore.HandleJSON(outbox.TopicOrderPlaced, d.notifier.SendOrderPlaced)
	outboxStore.HandleJSON(outbox.TopicPasswordReset, passwordResetHandler(account.NewStore(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicPasswordResetRequest, account.NewStore(d.pool).IssueReset)
	outboxStore.HandleJSON(outbox.TopicRegistration, registrationHandler(account.NewStore(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicOrderPaid, d.notifier.SendOrderPaid)
	outboxStore.HandleJSON(outbox.TopicOrderShipped, d.notifier.SendOrderShipped)
	outboxStore.HandleJSON(outbox.TopicOrderTerminal, terminalOrderHandler(ordernotice.NewRecipients(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicNewsletterConfirm, d.notifier.SendNewsletterConfirm)
	outboxStore.HandleJSON(outbox.TopicNewsletterWelcome, d.notifier.SendNewsletterWelcome)
	outboxStore.HandleJSON(outbox.TopicEmailVerify, addressVerifyHandler(account.NewStore(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicStaffInvitation, staffInvitationHandler(staff.NewStore(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicStaffEnrolment, d.notifier.SendStaffEnrolment)
	outboxStore.HandleJSON(outbox.TopicNewsletterIssue,
		newsletterIssueHandler(newsletter.NewStore(d.pool), d.notifier))
	outboxStore.HandleJSON(outbox.TopicRestocked, d.notifier.SendRestockNotice)
	outboxStore.HandleJSON(outbox.TopicInvoiceDue, invoiceDueHandler(d.admin, d.invoices))
	outboxStore.HandleJSON(outbox.TopicInvoiceVoidDue, invoiceVoidDueHandler(d.admin, d.invoices))
	return outboxStore
}

// invoiceDueHandler claims on the ADMIN pool: `store` holds no EXECUTE on the
// invoice doors.
func invoiceDueHandler(adminPool *pgxpool.Pool, gateway *invoice.Gateway) func(context.Context, *outbox.InvoiceDue) error {
	return invoice.NewStore(adminPool, gateway).ClaimDue
}

// invoiceVoidDueHandler claims on the ADMIN pool, as invoiceDueHandler does.
func invoiceVoidDueHandler(adminPool *pgxpool.Pool, gateway *invoice.Gateway) func(context.Context, *outbox.InvoiceVoidDue) error {
	return invoice.NewStore(adminPool, gateway).ClaimVoidDue
}

// newsletterIssueHandler delivers one copy of an issue, and asks at DELIVERY
// whether the address still wants it. The send freezes one outbox row per
// subscriber and the queue drains at bulk priority behind every transactional
// message — minutes to hours for a real list — so an unsubscribe committing
// anywhere in that window has to be read here rather than at the send.
func newsletterIssueHandler(
	subscribers *newsletter.Store,
	notifier email.Notifier,
) func(context.Context, *email.NewsletterIssue) error {
	return func(ctx context.Context, p *email.NewsletterIssue) error {
		wanted, err := subscribers.StillSubscribed(ctx, p.Email)
		if err != nil {
			return err
		}
		if !wanted {
			// Nothing left to do rather than a failure: sending is what must not
			// happen, and rescheduling would retry exactly that.
			return nil
		}
		return notifier.SendNewsletterIssue(ctx, p)
	}
}

// registrationHandler follows a registration up on the store pool, which is the
// one that wrote it: a new account's link is queued there, and an address that
// already had an account is told so by mail.
func registrationHandler(accounts *account.Store, notifier email.Notifier) func(context.Context, *outbox.AccountRegistration) error {
	return func(ctx context.Context, r *outbox.AccountRegistration) error {
		return accounts.FollowUpRegistration(ctx, r, notifier.SendAccountExists)
	}
}

func passwordResetHandler(accounts *account.Store, notifier email.Notifier) func(context.Context, *email.PasswordReset) error {
	return func(ctx context.Context, p *email.PasswordReset) error {
		return accounts.DeliverPasswordReset(ctx, p, notifier.SendPasswordReset)
	}
}

// addressVerifyHandler delivers a link to prove an address on the store pool,
// which is the one that wrote it: the link goes out, or, when the address is
// by now another account's, that account is told instead.
func addressVerifyHandler(accounts *account.Store, notifier email.Notifier) func(context.Context, *email.AddressVerify) error {
	return func(ctx context.Context, p *email.AddressVerify) error {
		return accounts.DeliverAddressVerify(ctx, p, notifier.SendAddressVerify, notifier.SendAccountExists)
	}
}

// staffInvitationHandler reads the recipient on the store pool: `store` already
// holds SELECT on users, and delivery writes nothing.
func staffInvitationHandler(roster *staff.Store, notifier email.Notifier) func(context.Context, *email.StaffInvitation) error {
	return func(ctx context.Context, p *email.StaffInvitation) error {
		address, name, err := roster.InvitationRecipient(ctx, p.UserID)
		if err != nil {
			return err
		}
		if address == "" {
			return nil
		}
		return notifier.SendStaffInvitation(ctx, p, address, name)
	}
}

func terminalOrderHandler(recipients ordernotice.Recipients, notifier email.Notifier) func(context.Context, *email.OrderTerminal) error {
	return func(ctx context.Context, p *email.OrderTerminal) error {
		to, ok, err := recipients.Of(ctx, p.OrderID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		return notifier.SendOrderTerminal(ctx, p, email.TerminalRecipient(to))
	}
}
