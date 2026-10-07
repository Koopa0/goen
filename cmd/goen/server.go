package main

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/coupons"
	customerdesk "github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/invoicing"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/reports"
	returndesk "github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/admin/shipping"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/invoice"
	rewards "github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/probe"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/returnpage"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/ui/pages"
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
	Invoices *invoice.Gateway
	// Google signs customers in, or is disabled and 404s its two routes.
	Google *account.Google
	// StoreMap is the carrier's convenience-store picker, or is disabled and
	// the checkout offers no pickup, the route is not registered, the
	// cross-origin defence gains no bypass, and the policy is unchanged.
	StoreMap *cart.StoreMap
	// DemoAccount is the account a public demonstration shares, or the zero
	// value and the sign-in page offers none.
	DemoAccount account.DemoAccount
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
	// SecureCookies selects the __Host- cookie prefix.
	SecureCookies bool
	// TOTPKey is parsed key material from twofactor.ParseKey; nil disables
	// enrolment.
	TOTPKey []byte
	// Invoices issues uniform invoices, or is disabled and renders no controls.
	Invoices *invoice.Gateway
	StoreMap *cart.StoreMap
}

func newRouter(cfg *RouterConfig, log *slog.Logger) http.Handler {
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

	probes := probe.NewHandler(log,
		probe.Dependency{Name: "storefront", DB: pool},
		probe.Dependency{Name: "admin", DB: adminPool},
	)
	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

	catalogue := catalog.NewStore(pool)
	siteStore := site.NewStore(pool)
	if !front.StoreMap.Enabled() {
		catalogue, siteStore = catalogue.WithoutPickup(), siteStore.WithoutPickup()
	}
	sitePages := site.NewHandler(log, baseURL, catalogue, siteStore, secureCookies)
	// Half of the order-lookup credential is a guessable order number, so
	// unlimited asking makes the endpoint an oracle for the other half.
	findLimit := ratelimit.New(ratelimit.Config{
		Every: 6 * time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 65_536,
	})
	var barcodeChecker cart.MobileBarcodeChecker
	if front.Invoices.Enabled() {
		barcodeChecker = front.Invoices
	}
	orderAccess := orderaccess.NewStore(pool, secureCookies)
	basketStore := cart.NewStore(pool)
	basket := cart.NewHandler(basketStore, orderAccess, log, secureCookies, findLimit,
		sessionCloser(front.Payments), front.StoreMap, barcodeChecker)
	customers := account.NewHandler(account.NewStore(pool), basket, log, secureCookies, front.Google)
	customers.OfferDemoAccount(front.DemoAccount)
	storefrontRoutes(mux, front, log, catalogue, sitePages, basket, customers, orderAccess, findLimit)
	backOfficeRoutes(mux, &cfg.BackOffice, log, poolsOnHealthPage(pool, adminPool, cfg.BackOffice.MaintenancePool))
	mux.HandleFunc("GET /", sitePages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse of
	// this order. The chrome middleware fills what every page shows, and the
	// locale is on the context before any handler or template reads it.
	var handler http.Handler = mux
	handler = withBanner(handler, home.NewStore(pool), log, secureCookies)
	handler = withTopNav(handler, home.NewStore(pool), catalogue, log)
	handler = withStaffEntrance(handler)
	handler = withSiteOrigin(handler, baseURL)
	handler = withNoStore(handler)
	handler = onlyVisitorPaths(func(next http.Handler) http.Handler {
		return withLocale(next, secureCookies)
	}, handler)
	handler = onlyVisitorPaths(basket.WithCount, handler)
	handler = onlyVisitorPaths(customers.Authenticate, handler)
	handler = withRequestBudget(handler)
	handler = crossOriginProtection(handler, front.StoreMap.Enabled())
	// Before routing and before every middleware that reads the request, so no
	// path value or query value PostgreSQL refuses reaches a query.
	handler = web.RefuseUnstorableText(handler, secureCookies)
	handler = securityHeaders(handler, policyWith(front.StoreMap.Origin()), secureCookies)
	handler = web.Compress(handler)
	return withRequestTracing(handler, log)
}

// storefrontRoutes registers the shop and the customer account, every handler
// on the `store` pool. The rest are built by the caller because the middleware
// chain and the catch-all read them too.
func storefrontRoutes(mux *http.ServeMux, cfg *StorefrontConfig, log *slog.Logger,
	catalogue *catalog.Store, sitePages *site.Handler, basket *cart.Handler,
	customers *account.Handler, orderAccess *orderaccess.Store, findLimit *ratelimit.Limiter) {
	pool, gateway := cfg.StorePool, cfg.Payments
	baseURL, secureCookies := cfg.BaseURL, cfg.SecureCookies
	authLimit := ratelimit.New(ratelimit.Config{
		Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
	})
	// Everything that describes shipping reads what checkout offers: pickup needs
	// the store map, so without it nothing may promise pickup or its price.
	homeStore, productStore := home.NewStore(pool), product.NewStore(pool, log)
	if !cfg.StoreMap.Enabled() {
		homeStore, productStore = homeStore.WithoutPickup(), productStore.WithoutPickup()
	}
	browse := catalog.NewHandler(catalogue, log)
	homePage := home.NewHandler(homeStore, log, secureCookies)
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
	points := rewards.NewHandler(rewards.NewStore(pool), log)
	items := product.NewHandler(productStore, log, baseURL)
	// Every checkout submission looks up the coupon it carries, and a chooser
	// change is a submission too, so a whole checkout is a dozen posts at most.
	// cart.Handler bounds the wrong codes themselves; this bounds the rest.
	checkoutLimit := ratelimit.New(ratelimit.Config{
		Every: 2 * time.Second, Burst: 30, TTL: time.Hour, MaxKeys: 65_536,
	})
	till := payment.NewHandler(payment.NewStore(pool), gateway, orderAccess, log)
	sendbacks := returnpage.NewHandler(returnpage.NewStore(pool), orderAccess, log)

	mux.HandleFunc("GET /{$}", homePage.Index)
	// The digest in the path is the only authorisation an image has, and it is
	// unguessable by construction.
	mux.HandleFunc("GET /media/{digest}", images.Serve)
	mux.HandleFunc("GET /media/{digest}/{width}", images.Serve)
	mux.HandleFunc("GET /sitemap.xml", sitePages.Sitemap)
	mux.HandleFunc("GET /robots.txt", sitePages.Robots)
	mux.HandleFunc("POST /promo/dismiss", homePage.Dismiss)
	mux.HandleFunc("POST /locale", sitePages.SetLocale)
	mux.HandleFunc("GET /faq", sitePages.FAQ)
	mux.HandleFunc("GET /shipping", sitePages.Shipping)
	mux.HandleFunc("GET /returns", sitePages.Policy)
	mux.HandleFunc("GET /payment", sitePages.Policy)
	mux.HandleFunc("GET /warranty", sitePages.Policy)
	mux.HandleFunc("GET /privacy", sitePages.Policy)
	mux.HandleFunc("GET /terms", sitePages.Policy)
	mux.HandleFunc("GET /about", sitePages.About)
	mux.HandleFunc("GET /contact", contactPage.Page)
	mux.HandleFunc("POST /contact", contactPage.Submit)
	// Submit applies the per-IP bound itself: Guard would answer plain text and
	// htmx would swap that over the footer form.
	mux.HandleFunc("POST /newsletter", signups.Submit)
	mux.HandleFunc("GET /newsletter/thanks", signups.Thanks)
	// Both links open a page carrying a form; a GET that confirmed or
	// unsubscribed would be completed by every link scanner that reads the mail
	// before its owner does.
	mux.HandleFunc("GET /newsletter/confirm", signups.ConfirmPage)
	mux.HandleFunc("POST /newsletter/confirm", signups.Confirm)
	mux.HandleFunc("GET /newsletter/unsubscribe", signups.UnsubscribePage)
	mux.HandleFunc("POST /newsletter/unsubscribe", signups.Unsubscribe)
	mux.HandleFunc("GET /c/{slug}", browse.Listing)
	mux.HandleFunc("GET /search", browse.Search)
	mux.HandleFunc("GET /compare", browse.Compare)
	mux.HandleFunc("GET /deals", browse.Deals)
	mux.HandleFunc("GET /s/{slug}", browse.Campaign)
	mux.HandleFunc("GET /p/{slug}", items.Detail)
	mux.HandleFunc("POST /p/{slug}/reviews", items.Review)
	mux.HandleFunc("POST /p/{slug}/notify", ratelimit.Guard(notifyLimit, log, items.Notify))
	mux.HandleFunc("POST /p/{slug}/questions", items.Ask)
	mux.HandleFunc("GET /cart", basket.Page)
	mux.HandleFunc("POST /cart/items", clearSpeculations(basket.AddItem))
	mux.HandleFunc("POST /cart/items/update", clearSpeculations(basket.UpdateItem))
	mux.HandleFunc("GET /checkout", basket.Checkout)
	mux.HandleFunc("POST /checkout", ratelimit.Guard(checkoutLimit, log, basket.PlaceOrder))
	if cfg.StoreMap.Enabled() {
		// The carrier's page posts the chosen store here from the SHOPPER'S
		// browser, so it arrives cross-site with none of goen's cookies. It
		// exists only where a carrier is configured: an unconfigured goen
		// answers 404 here and its cross-origin defence keeps no bypass.
		mux.HandleFunc("POST "+cart.PickupReturnPath, basket.PickupReturn)
		// The checkout form is posted here by 「選擇門市」: same-origin, so under
		// the cross-origin defence like every other form, and it keeps what was
		// typed before the browser is handed to the carrier.
		mux.HandleFunc("POST "+pages.PickupStartAction, ratelimit.Guard(checkoutLimit, log, basket.PickupStart))
		mux.HandleFunc("GET "+pages.PickupMapPath, basket.PickupMap)
	}
	mux.HandleFunc("GET /orders/find", basket.FindOrderPage)
	mux.HandleFunc("POST /orders/find", ratelimit.Guard(findLimit, log, basket.FindOrder))
	mux.HandleFunc("GET /orders/{number}", basket.OrderPage)
	mux.HandleFunc("POST /orders/{number}/cancel", basket.CancelOrder)
	mux.HandleFunc("POST /orders/{number}/reorder", clearSpeculations(basket.ReorderItems))
	mux.HandleFunc("GET /orders/{number}/pay", till.Page)
	mux.HandleFunc("POST /orders/{number}/pay", till.Start)
	mux.HandleFunc("GET /orders/{number}/return", sendbacks.Page)
	mux.HandleFunc("POST /orders/{number}/return", sendbacks.Submit)

	// Stripe posts server-to-server with no Origin, so the cross-origin
	// middleware has nothing to check and lets it through; its authentication is
	// the Stripe-Signature header the handler verifies.
	mux.HandleFunc("POST /webhooks/stripe", till.Webhook)

	mux.HandleFunc("GET /signin", customers.SignInPage)
	// GET on both: the callback is a redirect from Google whose method goen does
	// not choose, and nothing about an account changes until the callback has
	// matched the state it issued.
	mux.HandleFunc("GET /auth/google", customers.GoogleSignIn)
	mux.HandleFunc("GET /auth/google/callback", customers.GoogleCallback)
	// The limiter runs before argon2 does: at 64 MiB a hash, an unbounded
	// endpoint is a memory exhaustion anybody can trigger. One limiter across
	// them all, so moving between them earns no fresh allowance.
	mux.HandleFunc("POST /signin", clearSpeculations(ratelimit.Guard(authLimit, log, customers.SignIn)))
	mux.HandleFunc("GET /forgot", customers.ForgotPage)
	mux.HandleFunc("POST /forgot", ratelimit.Guard(authLimit, log, customers.Forgot))
	mux.HandleFunc("GET /reset", customers.ResetPage)
	mux.HandleFunc("POST /reset", ratelimit.Guard(authLimit, log, customers.Reset))
	// Open to a signed-out visitor, who is sent on rather than refused: a
	// registration link to the page that asks for its password, and a new
	// address's link to sign in first. The token proves only the mailbox, and
	// the address goes to the account that asked for it.
	mux.HandleFunc("GET /verify", customers.VerifyPage)
	mux.HandleFunc("POST /verify", ratelimit.Guard(authLimit, log, customers.Verify))
	mux.HandleFunc("GET /register", customers.RegisterPage)
	mux.HandleFunc("POST /register", clearSpeculations(ratelimit.Guard(authLimit, log, customers.Register)))
	mux.HandleFunc("POST /register/resend", ratelimit.Guard(authLimit, log, customers.ResendRegistration))
	// Open to a signed-out visitor: the link and the password chosen at
	// registration are the proof. Under authLimit because it runs
	// argon2, and it signs in, so it clears speculations.
	mux.HandleFunc("GET /register/complete", customers.CompleteRegistrationPage)
	mux.HandleFunc("POST /register/complete", clearSpeculations(ratelimit.Guard(authLimit, log, customers.CompleteRegistration)))
	mux.HandleFunc("POST /signout", clearSpeculations(clearCache(customers.SignOut)))
	mux.HandleFunc("GET /account", customers.RequireUser(customers.Overview))
	mux.HandleFunc("GET /account/cart-recovery", customers.RequireUser(customers.CartRecoveryPage))
	mux.HandleFunc("POST /account/cart/retry", customers.RequireUser(customers.RetryCartAdoption))
	mux.HandleFunc("GET /account/points", customers.RequireUser(points.Page))
	mux.HandleFunc("POST /account/points", customers.RequireUser(points.Redeem))
	mux.HandleFunc("GET /account/warranty", customers.RequireUser(cover.List))
	mux.HandleFunc("GET /account/warranty/{number}", customers.RequireUser(cover.Order))
	mux.HandleFunc("POST /account/warranty/{number}", customers.RequireUser(cover.Register))
	mux.HandleFunc("GET /account/wishlist", customers.RequireUser(customers.Wishlist))
	mux.HandleFunc("POST /account/wishlist", customers.RequireUser(customers.SaveWishlist))
	mux.HandleFunc("GET /account/orders/{number}", customers.RequireUser(customers.OrderPage))
	mux.HandleFunc("POST /account/profile", customers.RequireUser(customers.UpdateProfile))
	mux.HandleFunc("POST /account/addresses", customers.RequireUser(customers.AddAddress))
	mux.HandleFunc("POST /account/addresses/default", customers.RequireUser(customers.MakeDefaultAddress))
	mux.HandleFunc("POST /account/addresses/delete", customers.RequireUser(customers.DeleteAddress))
	// Also guarded: these verify a password with argon2, so a signed-in session
	// is otherwise an unbounded supply of 64 MiB hashes.
	mux.HandleFunc("POST /account/email",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ChangeEmail)))
	mux.HandleFunc("POST /account/email/resend",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ResendVerification)))
	mux.HandleFunc("POST /account/password",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ChangePassword)))
	mux.HandleFunc("POST /account/erase", customers.RequireUser(customers.Erase))
	mux.HandleFunc("POST /account/google/unlink", customers.RequireUser(customers.UnlinkGoogle))
}

// backOfficeRoutes registers /admin, every desk on the `admin` pool. The store
// pool reaches it only as statistics on the health page.
func backOfficeRoutes(mux *http.ServeMux, cfg *BackOfficeConfig, log *slog.Logger, healthPools []health.NamedPool) {
	adminPool, gateway, refunder := cfg.AdminPool, cfg.Payments, cfg.Refunder
	secureCookies, totpKey := cfg.SecureCookies, cfg.TOTPKey
	// The second factor runs on the ADMIN pool. On the storefront pool `store`
	// would need write on staff_totp_credentials and users.role, so any slip
	// reachable from a product page escalates to admin.
	factorStore := twofactor.NewStore(adminPool, totpKey)
	factors := twofactor.NewHandler(factorStore, log, secureCookies)
	var stepUp func(*http.Request) (bool, error)
	if factorStore.Enabled() {
		stepUp = factors.StepUp
	}
	// A cancellation withdraws an unsent issue even with no 加值中心, so the
	// invoice store exists either way.
	invoiceGateway := cfg.Invoices
	if invoiceGateway == nil {
		invoiceGateway = &invoice.Gateway{}
	}
	invoices := invoice.NewStore(adminPool, invoiceGateway)
	var invoiceWriter invoicing.Writer
	if invoices.Enabled() {
		invoiceWriter = invoices
	}
	adminImages := media.NewHandler(media.NewStore(adminPool), log)
	stockroom := stock.NewStore(adminPool)
	checkup := health.NewStore(adminPool).WithInvoicing(invoices.Enabled())
	payouts := refunds.NewStore(adminPool, refunder, invoices)
	invoicingStore := invoicing.NewStore(adminPool, invoices, invoiceWriter)
	salesFigures := reports.NewStore(adminPool)
	orderDesk := orders.NewHandler(orders.NewStore(adminPool, payouts, invoicingStore, stockroom, checkup, salesFigures), sessionCloser(gateway), log)
	trail := audit.NewHandler(audit.NewStore(adminPool), log)
	figures := reports.NewHandler(salesFigures, log)
	warehouse := stock.NewHandler(stockroom, log)
	catalogueDesk := products.NewHandler(products.NewStore(adminPool), adminImages, log)
	refundDesk := refunds.NewHandler(payouts, sessionCloser(gateway), log)
	returnDesk := returndesk.NewHandler(returndesk.NewStore(adminPool, payouts), log)
	invoiceDesk := invoicing.NewHandler(invoicingStore, log)
	delivery := shipping.NewHandler(shipping.NewStore(adminPool), cfg.StoreMap.Enabled(), log)
	brands := taxonomy.NewHandler(taxonomy.NewStore(adminPool), adminImages, log)
	shopfront := content.NewHandler(content.NewStore(adminPool), adminImages, newsletter.NewStore(adminPool), log)
	sales := campaigns.NewHandler(campaigns.NewStore(adminPool), adminImages, log)
	inbox := feedback.NewHandler(feedback.NewStore(adminPool), log)
	roster := staff.NewHandler(staff.NewStore(adminPool), log, factorStore.Enabled())
	lookup := customerdesk.NewHandler(customerdesk.NewStore(adminPool), log)
	promotions := coupons.NewHandler(coupons.NewStore(adminPool), log)
	programme := loyalty.NewHandler(loyalty.NewStore(adminPool), log)
	workers := health.NewHandler(checkup, outbox.NewStore(adminPool, log),
		healthPools, disputeSource(gateway), log)

	// The back office. A signed-in customer gets a 404 rather than a 403, which
	// would confirm that /admin is a real place.
	backOffice := access.New(log, stepUp).WithHealthTaskCount(checkup.StaffTaskCount)
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
	shopfront.Routes(mux, backOffice)
	brands.Routes(mux, backOffice)
	delivery.Routes(mux, backOffice)
	warehouse.Routes(mux, backOffice)
	catalogueDesk.Routes(mux, backOffice)
	refundDesk.Routes(mux, backOffice)
	returnDesk.Routes(mux, backOffice)
	invoiceDesk.Routes(mux, backOffice)
	mux.HandleFunc("GET /admin/verify", backOffice.StaffOnly(factors.Challenge))
	mux.HandleFunc("POST /admin/verify", backOffice.StaffOnly(factors.Verify))
	mux.HandleFunc("POST /admin/verify/enrol", backOffice.StaffOnly(factors.Enrol))
	mux.HandleFunc("POST /admin/verify/confirm", backOffice.StaffOnly(factors.Confirm))
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
