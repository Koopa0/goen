package main

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/account"
	adminaccess "github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/banners"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/coupons"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/dashboard"
	"github.com/koopa0/goen/internal/admin/faq"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/admin/health"
	adminloyalty "github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/newsletters"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/slides"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/admin/twofactor"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/probe"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/warranty"
	"github.com/koopa0/goen/internal/web"
)

// contentSecurityPolicy is strict: goen renders no inline script and no inline
// style, serves its own fonts, and names no third-party origin a browser may
// fetch from. The one third party left is where a payment form is allowed to
// post, which is a destination rather than a source.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"font-src 'self'; " +
	"img-src 'self' data:; " +
	// Browsers have applied form-action to the redirect a submission lands on,
	// and goen answers the payment form with a 303 to checkout.stripe.com.
	"form-action 'self' https://checkout.stripe.com; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// formActionDirective is the one directive a configured store map widens, and
// the only place that widening may happen.
const formActionDirective = "form-action 'self' https://checkout.stripe.com"

// policyWith is the policy this deployment sends. With no store map configured
// it is contentSecurityPolicy itself, byte for byte; with one it names exactly
// one more origin, and only as a form DESTINATION.
func policyWith(mapOrigin string) string {
	if mapOrigin == "" {
		return contentSecurityPolicy
	}
	return strings.Replace(contentSecurityPolicy, formActionDirective,
		formActionDirective+" "+mapOrigin, 1)
}

// RouterConfig is what the router needs, one half per pool. Each half holds
// only what its routes read, so the storefront cannot reach the admin pool nor
// the back office the store pool; a field both halves use is set in both.
type RouterConfig struct {
	Storefront StorefrontConfig
	BackOffice BackOfficeConfig
}

// StorefrontConfig is what the shop and the customer account need. StorePool is
// required; every provider below may be nil, and the feature it serves then
// disables itself and says so.
type StorefrontConfig struct {
	// StorePool runs as `store`.
	StorePool *pgxpool.Pool
	// Payments is Stripe, or is disabled and the payment page says so.
	Payments *payment.Gateway
	BaseURL  string
	// SecureCookies selects the __Host- cookie prefix.
	SecureCookies bool
	// Invoices checks mobile barcodes at checkout, or is disabled and the
	// checkout accepts none.
	Invoices *issuer.Gateway
	// Google signs customers in, or is disabled and 404s its two routes.
	Google *auth.Google
	// StoreMap is the carrier's convenience-store picker, or is disabled and
	// the checkout offers no pickup, the route is not registered, the
	// cross-origin defence gains no bypass, and the policy is unchanged.
	StoreMap *cart.StoreMap
	// DemoAccount is the account a public demonstration shares, or the zero
	// value and the sign-in page offers none.
	DemoAccount auth.DemoAccount
}

// BackOfficeConfig is what /admin needs. AdminPool is required; every provider
// below may be nil, and the feature it serves then disables itself and says so.
type BackOfficeConfig struct {
	// AdminPool runs as `admin`.
	AdminPool *pgxpool.Pool
	// MaintenancePool is the background workers' pool. It serves no request;
	// it is here only so the health page can show its connection statistics,
	// and nil leaves it off the page.
	MaintenancePool *pgxpool.Pool
	Payments        *payment.Gateway
	Refunder        refunds.Refunder
	// TOTPKey is parsed key material from twofactor.ParseKey; nil disables
	// enrolment.
	TOTPKey []byte
	// TOTPPreviousKey is the key TOTPKey replaced, parsed the same way, or nil.
	// Secrets sealed under it still open and are re-sealed under TOTPKey on the
	// owner's next sign-in.
	TOTPPreviousKey []byte
	// Invoices issues uniform invoices, or is disabled and renders no controls.
	Invoices *issuer.Gateway
	StoreMap *cart.StoreMap
}

func newRouter(cfg *RouterConfig, log *slog.Logger) http.Handler {
	handler, _ := routing(cfg, log)
	return handler
}

// routing is newRouter's handler together with the mux inside it. The mux is
// what picks a request's route, and [http.ServeMux.Handler] names the pattern
// it picks, so a test can ask production's own routing where a URL lands
// rather than a list of patterns read from the source.
func routing(cfg *RouterConfig, log *slog.Logger) (http.Handler, *http.ServeMux) {
	// As struct fields the two pools can be omitted silently, and they sit
	// beside Payments, Invoices, Google and TOTPKey, which may legitimately be nil.
	if cfg == nil || cfg.Storefront.StorePool == nil || cfg.BackOffice.AdminPool == nil || log == nil {
		panic("goen: newRouter requires both pools and a logger")
	}
	front := &cfg.Storefront
	pool, adminPool := front.StorePool, cfg.BackOffice.AdminPool
	baseURL, secureCookies := front.BaseURL, front.SecureCookies

	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, staticAssetHandler(assets.Handler(log)))
	mux.HandleFunc("GET /favicon.ico", staticAssetHandler(assets.Alias(log, assets.FaviconICO)).ServeHTTP)

	probes := probe.NewHandler(log, pool, schemaCheck(pool))
	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

	catalogue := catalog.NewStore(pool, log)
	siteStore := site.NewStore(pool)
	if !front.StoreMap.Enabled() {
		catalogue, siteStore = catalogue.WithoutPickup(), siteStore.WithoutPickup()
	}
	sitePages := site.NewHandler(log, baseURL, catalogue, siteStore, secureCookies)
	var barcodeChecker cart.MobileBarcodeChecker
	if front.Invoices.Enabled() {
		barcodeChecker = front.Invoices
	}
	orderAccess := access.NewStore(pool, secureCookies)
	basketStore := cart.NewStore(pool)
	basket := cart.NewHandler(basketStore, orderAccess, log, secureCookies,
		sessionCloser(front.Payments), front.StoreMap, barcodeChecker)
	authn := auth.NewHandler(auth.NewStore(pool), basket, orderAccess, log, secureCookies, front.Google)
	authn.OfferDemoAccount(front.DemoAccount)
	// One store decides whether this deployment has a second factor at all, so
	// /admin and the storefront's account doors cannot disagree about it.
	factorStore := twofactor.NewStore(adminPool, cfg.BackOffice.TOTPKey, cfg.BackOffice.TOTPPreviousKey)
	authn.RequireStepUpFromStaff(storefrontStepUp(factorStore, pool, log))
	accounts := account.NewHandler(account.NewStore(pool), log, secureCookies, basket.TakesPayment())
	authn.RefuseOnAccountPage(accounts.Refuse)
	storefrontRoutes(mux, front, log, catalogue, sitePages, basket, authn, accounts, orderAccess)
	backOfficeRoutes(mux, &cfg.BackOffice, factorStore, log, poolsOnHealthPage(pool, adminPool, cfg.BackOffice.MaintenancePool))
	mux.HandleFunc("GET /", sitePages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse of
	// this order. The chrome middleware fills what every page shows, and the
	// locale is on the context before any handler or template reads it.
	var handler http.Handler = mux
	handler = withBanner(handler, sitePages.WithBanner)
	handler = withTopNav(handler, sitePages.WithTopNav)
	handler = withStaffEntrance(handler)
	handler = withSiteOrigin(handler, baseURL)
	handler = withNoStore(handler)
	handler = onlyVisitorPaths(func(next http.Handler) http.Handler {
		return withLocale(next, secureCookies)
	}, handler)
	handler = onlyVisitorPaths(basket.WithCount, handler)
	handler = onlyVisitorPaths(authn.Authenticate, handler)
	handler = withRequestBudget(handler)
	handler = crossOriginProtection(handler, front.StoreMap.Enabled())
	// Before routing and before every middleware that reads the request, so no
	// path value or query value PostgreSQL refuses reaches a query.
	handler = web.RefuseUnstorableText(handler, secureCookies, http.HandlerFunc(sitePages.BadRequest))
	handler = securityHeaders(handler, policyWith(front.StoreMap.Origin()), secureCookies)
	handler = web.Compress(handler)
	return withRequestTracing(handler, log), mux
}

// storefrontRoutes builds the shop and the customer account, every handler on
// the `store` pool, and has each register its own routes. The rest are built
// by the caller because the middleware chain and the catch-all read them too.
// The per-client budgets are decided here and handed to the routes they bound;
// clearSpeculations and clearCache are this deployment's, handed to the writes
// that need them; RequireUser is sign-in's, handed to the account's pages.
func storefrontRoutes(mux *http.ServeMux, cfg *StorefrontConfig, log *slog.Logger,
	catalogue *catalog.Store, sitePages *site.Handler, basket *cart.Handler,
	authn *auth.Handler, accounts *account.Handler, orderAccess *access.Store) {
	pool, gateway := cfg.StorePool, cfg.Payments
	baseURL := cfg.BaseURL
	// Every route that runs argon2 or mails an address: signing in, registering,
	// recovering an account, and changing a password or an email address.
	authLimit := ratelimit.New(ratelimit.Config{
		Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
	})
	// Everything that describes shipping reads what checkout offers: pickup needs
	// the store map, so without it nothing may promise pickup or its price.
	homeStore := home.NewStore(pool)
	if !cfg.StoreMap.Enabled() {
		homeStore = homeStore.WithoutPickup()
	}
	browse := catalog.NewHandler(catalogue, log, baseURL)
	homePage := home.NewHandler(homeStore, log)
	images := media.NewHandler(media.NewStore(pool), log)
	contactLimit := ratelimit.New(ratelimit.Config{
		Every: 5 * time.Minute, Burst: 5, TTL: time.Hour, MaxKeys: 65_536,
	})
	contactPage := contact.NewHandler(contact.NewStore(pool), contactLimit, log)
	// Restock notify is the same anonymous mailbox door as /contact, without
	// the contact form's other fields. A shared bucket would let one door
	// spend the other's budget.
	notifyLimit := ratelimit.New(ratelimit.Config{
		Every: 5 * time.Minute, Burst: 5, TTL: time.Hour, MaxKeys: 65_536,
	})
	// Tighter than authLimit: this one defends somebody else's mailbox.
	signupLimit := ratelimit.New(ratelimit.Config{
		Every: 10 * time.Minute, Burst: 2, TTL: time.Hour, MaxKeys: 65_536,
	})
	// The customer side writes newsletter_subscribers as `store`; composing and
	// sending write newsletter_issues and an audit row, which `store` may not,
	// so the back office gets its own store on the admin pool below.
	signups := newsletter.NewHandler(newsletter.NewStore(pool), signupLimit, log)
	cover := warranty.NewHandler(warranty.NewStore(pool), log)
	points := loyalty.NewHandler(loyalty.NewStore(pool), log)
	// Every checkout submission looks up the coupon it carries, and a chooser
	// change is a submission too, so a whole checkout is a dozen posts at most.
	// cart.Handler bounds the wrong codes themselves; this bounds the rest.
	checkoutLimit := ratelimit.New(ratelimit.Config{
		Every: 2 * time.Second, Burst: 30, TTL: time.Hour, MaxKeys: 65_536,
	})
	till := payment.NewHandler(payment.NewStore(pool), gateway, orderAccess, log)
	// Half of the order-lookup credential is a guessable order number, so
	// unlimited asking makes the endpoint an oracle for the other half.
	findLimit := ratelimit.New(ratelimit.Config{
		Every: 6 * time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 65_536,
	})
	buyerOrders := buyer.NewHandler(buyer.NewStore(pool), orderAccess, log, findLimit, basket.TakesPayment())

	homePage.Routes(mux)
	images.Routes(mux)
	sitePages.Routes(mux)
	contactPage.Routes(mux)
	signups.Routes(mux)
	browse.Routes(mux, notifyLimit)
	basket.Routes(mux, checkoutLimit, clearSpeculations)
	buyerOrders.Routes(mux)
	till.Routes(mux)
	authn.Routes(mux, authLimit, clearSpeculations, clearCache)
	accounts.Routes(mux, authn.RequireUser)
	points.Routes(mux, authn.RequireUser)
	cover.Routes(mux, authn.RequireUser)
}

// backOfficeRoutes registers /admin, every desk on the `admin` pool. The store
// pool reaches it only as statistics on the health page.
func backOfficeRoutes(mux *http.ServeMux, cfg *BackOfficeConfig, factorStore *twofactor.Store, log *slog.Logger, healthPools []health.NamedPool) {
	adminPool, gateway, refunder := cfg.AdminPool, cfg.Payments, cfg.Refunder
	// factorStore (built in newRouter) runs the second factor on the ADMIN pool.
	// On the storefront pool `store`
	// would need write on staff_totp_credentials and users.role, so any slip
	// reachable from a product page escalates to admin.
	factors := twofactor.NewHandler(factorStore, log)
	var stepUp func(*http.Request) (bool, error)
	if factorStore.Enabled() {
		stepUp = factors.StepUp
	}
	// A cancellation withdraws an unsent issue even with no 加值中心, so the
	// invoice store exists either way.
	invoiceGateway := cfg.Invoices
	if invoiceGateway == nil {
		invoiceGateway = &issuer.Gateway{}
	}
	invoices := issuer.NewStore(adminPool, invoiceGateway)
	var invoiceWriter orders.InvoiceWriter
	if invoices.Enabled() {
		invoiceWriter = invoices
	}
	adminImages := media.NewHandler(media.NewStore(adminPool), log)
	stockroom := stock.NewStore(adminPool)
	checkup := health.NewStore(adminPool).WithInvoicing(invoices.Enabled())
	payouts := refunds.NewStore(adminPool, refunder, invoices)
	salesFigures := reports.NewStore(adminPool, stockroom)
	orderDesk := orders.NewHandler(orders.NewStore(adminPool, payouts, invoices, invoiceWriter), sessionCloser(gateway), log)
	landing := dashboard.NewHandler(dashboard.NewStore(adminPool, stockroom, checkup, salesFigures), log)
	trail := audit.NewHandler(audit.NewStore(adminPool), log)
	figures := reports.NewHandler(salesFigures, log)
	warehouse := stock.NewHandler(stockroom, log)
	catalogueDesk := products.NewHandler(products.NewStore(adminPool), adminImages, log)
	refundDesk := refunds.NewHandler(payouts, sessionCloser(gateway), log)
	returnDesk := returns.NewHandler(returns.NewStore(adminPool, payouts), log)
	delivery := shipping.NewHandler(shipping.NewStore(adminPool), cfg.StoreMap.Enabled(), log)
	brands := taxonomy.NewHandler(taxonomy.NewStore(adminPool), adminImages, log)
	slideDesk := slides.NewHandler(slides.NewStore(adminPool, home.NewStore(adminPool)), adminImages, log)
	bannerDesk := banners.NewHandler(banners.NewStore(adminPool), log)
	issueDesk := newsletters.NewHandler(newsletters.NewStore(adminPool), log)
	faqDesk := faq.NewHandler(faq.NewStore(adminPool), log)
	sales := campaigns.NewHandler(campaigns.NewStore(adminPool), adminImages, log)
	inbox := feedback.NewHandler(feedback.NewStore(adminPool), log)
	roster := staff.NewHandler(staff.NewStore(adminPool), log, factorStore.Enabled())
	lookup := customers.NewHandler(customers.NewStore(adminPool), log)
	promotions := coupons.NewHandler(coupons.NewStore(adminPool), log)
	programme := adminloyalty.NewHandler(adminloyalty.NewStore(adminPool), log)
	workers := health.NewHandler(checkup, outbox.NewStore(adminPool, log),
		healthPools, disputeSource(gateway), log)

	// The back office. A signed-in customer gets a 404 rather than a 403, which
	// would confirm that /admin is a real place.
	backOffice := adminaccess.New(log, stepUp).WithHealthTaskCount(checkup.StaffTaskCount)
	landing.Routes(mux, backOffice)
	orderDesk.Routes(mux, backOffice)
	trail.Routes(mux, backOffice)
	figures.Routes(mux, backOffice)
	workers.Routes(mux, backOffice)
	programme.Routes(mux, backOffice)
	promotions.Routes(mux, backOffice)
	lookup.Routes(mux, backOffice)
	roster.Routes(mux, backOffice)
	inbox.Routes(mux, backOffice)
	sales.Routes(mux, backOffice)
	slideDesk.Routes(mux, backOffice)
	bannerDesk.Routes(mux, backOffice)
	issueDesk.Routes(mux, backOffice)
	faqDesk.Routes(mux, backOffice)
	brands.Routes(mux, backOffice)
	delivery.Routes(mux, backOffice)
	warehouse.Routes(mux, backOffice)
	catalogueDesk.Routes(mux, backOffice)
	refundDesk.Routes(mux, backOffice)
	returnDesk.Routes(mux, backOffice)
	factors.Routes(mux, backOffice)
}

// storefrontStepUp is the back office's own question, "did this session pass
// the second factor?", asked for the storefront's account doors: the same
// twofactor.Handler.StepUp, so the two cannot drift. It reads over the store
// pool, which holds SELECT on sessions and never sees the TOTP key or a
// credential row, so the storefront still cannot reach the admin pool. It is
// nil when factors reports no key, which is /admin's own "second factor off".
func storefrontStepUp(factors *twofactor.Store, storePool *pgxpool.Pool, log *slog.Logger) func(*http.Request) (bool, error) {
	if !factors.Enabled() {
		return nil
	}
	return twofactor.NewHandler(twofactor.NewStore(storePool, nil, nil), log).StepUp
}

// sessionCloser hands the gateway to the cancel doors, or nothing at all.
// It returns an explicitly nil interface rather than a nil *Gateway, because a
// typed nil in an interface is non-nil and each handler's nil check would miss.
func sessionCloser(g *payment.Gateway) payment.SessionCloser {
	if g == nil || !g.Enabled() {
		return nil
	}
	return g
}

// disputeSource is the gateway as the health page's dispute source, or an
// explicitly nil interface when Stripe is not configured.
func disputeSource(g *payment.Gateway) health.DisputeSource {
	if g == nil || !g.Enabled() {
		return nil
	}
	return g
}

// poolsOnHealthPage names the pools whose statistics /admin/health shows. A nil
// pool is left off rather than shown as an empty row.
func poolsOnHealthPage(store, adminPool, maintenance *pgxpool.Pool) []health.NamedPool {
	var out []health.NamedPool
	for _, p := range []health.NamedPool{
		{Name: "store", Pool: store}, {Name: "admin", Pool: adminPool}, {Name: "maintenance", Pool: maintenance},
	} {
		if p.Pool != nil {
			out = append(out, p)
		}
	}
	return out
}
