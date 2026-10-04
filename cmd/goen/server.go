package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
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
	probe "github.com/koopa0/goen/internal/health"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	rewards "github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/ui/layouts"
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

// RouterConfig is what the router needs: the pools it serves from, the
// providers its handlers call, and the configuration they read. Pool, AdminPool
// and the logger are required; every provider below may be nil, and the feature
// it serves then disables itself and says so.
type RouterConfig struct {
	// Pool runs as `store` and AdminPool as `admin`; which one a handler gets
	// is the privilege boundary, not a performance choice.
	Pool      *pgxpool.Pool
	AdminPool *pgxpool.Pool
	// MaintenancePool is the background workers' pool. It serves no request;
	// it is here only so the health page can show its connection statistics,
	// and nil leaves it off the page.
	MaintenancePool *pgxpool.Pool
	// Payments is Stripe, or is disabled and the payment page says so.
	Payments *payment.Gateway
	Refunder refunds.Refunder
	BaseURL  string
	// SecureCookies selects the __Host- cookie prefix.
	SecureCookies bool
	// TOTPKey is parsed key material from twofactor.ParseKey; nil disables
	// enrolment.
	TOTPKey []byte
	// Invoices issues uniform invoices, or is disabled and renders no controls.
	Invoices *invoice.Gateway
	// Google signs customers in, or is disabled and 404s its two routes.
	Google *account.Google
	// StoreMap is the carrier's convenience-store picker, or is disabled and
	// the checkout asks for a chain alone, the route is not registered, the
	// cross-origin defence gains no bypass, and the policy is unchanged.
	StoreMap *cart.StoreMap
	// DemoAccount is the account a public demonstration shares, or the zero
	// value and the sign-in page offers none.
	DemoAccount account.DemoAccount
}

func newRouter(cfg *RouterConfig, log *slog.Logger) http.Handler {
	// As struct fields the two pools can be omitted silently, and they sit
	// beside Payments, Invoices, Google and TOTPKey, which may legitimately be nil.
	if cfg == nil || cfg.Pool == nil || cfg.AdminPool == nil || log == nil {
		panic("goen: newRouter requires both pools and a logger")
	}
	pool, adminPool := cfg.Pool, cfg.AdminPool
	gateway, refunder := cfg.Payments, cfg.Refunder
	baseURL, secureCookies, totpKey := cfg.BaseURL, cfg.SecureCookies, cfg.TOTPKey
	authLimit := ratelimit.New(ratelimit.Config{
		Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
	})
	catalogue := catalog.NewStore(pool)
	browse := catalog.NewHandler(catalogue, log)
	// Everything that describes shipping reads what checkout offers: pickup needs
	// the store map, so without it nothing may promise pickup or its price.
	siteStore, homeStore, productStore := site.NewStore(pool), home.NewStore(pool), product.NewStore(pool)
	if !cfg.StoreMap.Enabled() {
		siteStore, homeStore, productStore = siteStore.WithoutPickup(), homeStore.WithoutPickup(), productStore.WithoutPickup()
	}
	sitePages := site.NewHandler(log, baseURL, catalogue, siteStore, secureCookies)
	homePage := home.NewHandler(homeStore, log, secureCookies)
	probes := probe.NewHandler(log,
		probe.Dependency{Name: "storefront", DB: pool},
		probe.Dependency{Name: "admin", DB: adminPool},
	)
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
	// Half of the order-lookup credential is a guessable order number, so
	// unlimited asking makes the endpoint an oracle for the other half.
	findLimit := ratelimit.New(ratelimit.Config{
		Every: 6 * time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 65_536,
	})
	// Every checkout submission looks up the coupon it carries, and a chooser
	// change is a submission too, so a whole checkout is a dozen posts at most.
	// cart.Handler bounds the wrong codes themselves; this bounds the rest.
	checkoutLimit := ratelimit.New(ratelimit.Config{
		Every: 2 * time.Second, Burst: 30, TTL: time.Hour, MaxKeys: 65_536,
	})
	var barcodeChecker cart.MobileBarcodeChecker
	if cfg.Invoices.Enabled() {
		barcodeChecker = cfg.Invoices
	}
	basketStore := cart.NewStore(pool)
	basket := cart.NewHandler(basketStore, log, secureCookies, findLimit,
		sessionCloser(gateway), cfg.StoreMap, barcodeChecker)
	customers := account.NewHandler(account.NewStore(pool), basket, log, secureCookies, cfg.Google)
	customers.OfferDemoAccount(cfg.DemoAccount)
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
	var invoiceReader invoicing.Reader
	var invoiceWriter invoicing.Writer
	if invoices.Enabled() {
		invoiceReader = invoices
		invoiceWriter = invoices
	}
	adminImages := media.NewHandler(media.NewStore(adminPool), log)
	back := admin.NewHandler(admin.HandlerDeps{
		Store:    admin.NewStore(adminPool, refunder, invoiceReader, invoiceWriter),
		Log:      log,
		Sessions: sessionCloser(gateway),
	})
	trail := audit.NewHandler(audit.NewStore(adminPool), log)
	figures := reports.NewHandler(reports.NewStore(adminPool), log)
	warehouse := stock.NewHandler(stock.NewStore(adminPool), log)
	catalogueDesk := products.NewHandler(products.NewStore(adminPool), adminImages, log)
	payouts := refunds.NewStore(adminPool, refunder, invoices)
	refundDesk := refunds.NewHandler(payouts, sessionCloser(gateway), log)
	returnDesk := returndesk.NewHandler(returndesk.NewStore(adminPool, payouts), log)
	invoiceDesk := invoicing.NewHandler(invoicing.NewStore(adminPool, invoiceReader, invoiceWriter), log)
	delivery := shipping.NewHandler(shipping.NewStore(adminPool), cfg.StoreMap, log)
	brands := taxonomy.NewHandler(taxonomy.NewStore(adminPool), adminImages, log)
	shopfront := content.NewHandler(content.NewStore(adminPool), adminImages, newsletter.NewStore(adminPool), log)
	sales := campaigns.NewHandler(campaigns.NewStore(adminPool), adminImages, log)
	inbox := feedback.NewHandler(feedback.NewStore(adminPool), log)
	roster := staff.NewHandler(staff.NewStore(adminPool), log, factorStore.Enabled())
	lookup := customerdesk.NewHandler(customerdesk.NewStore(adminPool), log)
	promotions := coupons.NewHandler(coupons.NewStore(adminPool), log)
	programme := loyalty.NewHandler(loyalty.NewStore(adminPool), log)
	workers := health.NewHandler(health.NewStore(adminPool), outbox.NewStore(adminPool, log),
		poolsOnHealthPage(pool, adminPool, cfg.MaintenancePool), log)
	// basketStore answers the order-access question for all three packages.
	till := payment.NewHandler(payment.NewStore(pool), gateway, basketStore, log, secureCookies)
	sendbacks := returns.NewHandler(returns.NewStore(pool), basketStore, log, secureCookies)

	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, staticAssetHandler(assets.Handler(log)))
	mux.HandleFunc("GET /favicon.ico", staticAssetHandler(assets.Alias(log, assets.FaviconICO)).ServeHTTP)

	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

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

	// The back office. A signed-in customer gets a 404 rather than a 403, which
	// would confirm that /admin is a real place.
	backOffice := access.New(log, stepUp)
	back.Routes(mux, backOffice)
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

	mux.HandleFunc("GET /", sitePages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse of
	// this order. The chrome middleware fills what every page shows, and the
	// locale is on the context before any handler or template reads it.
	var handler http.Handler = mux
	handler = withBanner(handler, home.NewStore(pool), log, secureCookies)
	handler = withTopNav(handler, home.NewStore(pool), log)
	handler = withStaffEntrance(handler)
	handler = withSiteOrigin(handler, baseURL)
	handler = withNoStore(handler)
	handler = onlyVisitorPaths(func(next http.Handler) http.Handler {
		return withLocale(next, secureCookies)
	}, handler)
	handler = onlyVisitorPaths(basket.WithCount, handler)
	handler = onlyVisitorPaths(customers.Authenticate, handler)
	handler = withStorefrontRequestBudget(handler)
	handler = crossOriginProtection(handler, cfg.StoreMap.Enabled())
	// Before routing and before every middleware that reads the request, so no
	// path value or query value PostgreSQL refuses reaches a query.
	handler = web.RefuseUnstorableText(handler, secureCookies)
	handler = securityHeaders(handler, policyWith(cfg.StoreMap.Origin()), secureCookies)
	handler = web.Compress(handler)
	return withRequestTracing(handler, log)
}

// withRequestTracing wraps a handler with the request log, panic recovery,
// and identifier middleware. withRequestID is outermost so recovery sees the
// same context the response header was stamped from. Recovery sits inside the
// request log, so a request that panicked is logged with the 500 recovery sent.
func withRequestTracing(next http.Handler, log *slog.Logger) http.Handler {
	next = recoverPanic(next, log)
	next = requestLog(next, log)
	return withRequestID(next)
}

// staticAssetHandler leaves identity-versus-gzip selection with assets.Handler.
// The outer dynamic compressor must not invent a representation the immutable
// asset catalogue deliberately omitted because it did not shrink.
func staticAssetHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.NoCompress(w)
		next.ServeHTTP(w, r)
	})
}

// withStorefrontRequestBudget bounds pool acquisition and every database round
// trip on a visitor request, including the chrome middleware that shares the
// storefront pool. Stateless routes skip it: probes and webhooks must not spend
// a budget they never use.
func withStorefrontRequestBudget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statelessPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), storeRequestBudget)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// crossOriginProtection rejects cross-site form posts using the browser's own
// Sec-Fetch-Site signal, which is why goen's forms carry no CSRF token.
//
// pickupReturn adds the one bypass goen has, and only where a carrier is
// configured. The store map answers by having the shopper's own browser post to
// goen from the carrier's page, which is a cross-site POST by construction and
// cannot be made anything else. The pattern is matched exactly: a trailing
// slash or a cleaned path is a redirect to this pattern rather than this
// pattern, and net/http does not admit those. The handler behind it is written
// to be worth nothing to whoever drives it.
//
// A fresh CrossOriginProtection per call, never one hoisted to a package
// variable: AddInsecureBypassPattern panics on a pattern it already holds.
func crossOriginProtection(next http.Handler, pickupReturn bool) http.Handler {
	protection := http.NewCrossOriginProtection()
	if pickupReturn {
		protection.AddInsecureBypassPattern("POST " + cart.PickupReturnPath)
	}
	return protection.Handler(next)
}

// withRequestID gives every request an identifier and echoes it back. A
// client-supplied X-Request-Id is honoured only when it looks like an id: an
// arbitrary one reaches the logs, where a newline could forge a log line.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(web.WithRequestID(r.Context(), id)))
	})
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		alphanumeric := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !alphanumeric && c != '-' {
			return false
		}
	}
	return true
}

// strictTransportSecurity tells a browser that has reached goen over https to
// use nothing else for a year, on this host and its subdomains, so a network
// that rewrites an http:// link cannot keep a visitor in cleartext to read a
// password, a second-factor code or an address. preload is left to the owner:
// it is hard to undo.
const strictTransportSecurity = "max-age=31536000; includeSubDomains"

// securityHeaders sets what every response carries. HSTS goes only with secure
// cookies: development serves plain http, and a browser that once saw the
// header on localhost would refuse http there for a year.
func securityHeaders(next http.Handler, policy string, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if secure {
			h.Set("Strict-Transport-Security", strictTransportSecurity)
		}
		if speculates(r) {
			h.Set("Speculation-Rules", `"`+assets.URL(assets.SpeculationRules)+`"`)
		}
		next.ServeHTTP(w, r)
	})
}

// unspeculated are the paths goen will not offer speculation rules from.
//
// The rules themselves already refuse to prerender a link into any of these —
// the document is the guard, and this is the second half of the same decision:
// a page a visitor reaches only after signing in, or one that is a step in
// paying, offers no rules at all. Nothing is gained by speculating from a page
// somebody is reading once, and a bug in the rules costs more there than
// anywhere else on the site.
//
// Prefixes, because every one of them owns its whole subtree.
var unspeculated = []string{
	"/checkout",
	"/orders/",
	"/account",
	"/admin",
	"/signin",
	"/register",
	"/reset",
	"/verify",
}

// clearSpeculations tells a browser holding speculative copies of this site to
// throw them away after a write, because a copy is a whole document taken
// before it: its header would show the cart's count from before an add.
//
// Only the writes that change what the shared chrome says carry it: the cart's
// count and whether the visitor is staff. A wishlist write changes neither, and
// /account* is never speculated.
//
// A browser that ignores the directives is a count one behind for one page
// view; speculating /cart instead would show an empty cart to somebody who had
// just filled it.
func clearSpeculations(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// Only on a write. On a page response this would throw away the
			// speculations the visitor's own browsing has just earned.
			w.Header().Set("Clear-Site-Data", speculationCaches)
		}
		next(w, r)
	}
}

// speculationCaches are the Clear-Site-Data directives clearSpeculations sends.
const speculationCaches = `"prefetchCache", "prerenderCache"`

// clearCache is sign-out's addition to clearSpeculations: it asks the browser
// to empty this site's cache as the session ends. withNoStore is what keeps a
// signed-in page out of the caches; this is the second line, for a page a
// browser kept regardless. It is sign-out's alone because it discards every
// cached asset too, which the visitor then downloads again. Its value repeats
// the speculation directives, so the header is complete whichever wrapper sets
// it last.
func clearCache(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Clear-Site-Data", `"cache", `+speculationCaches)
		}
		next(w, r)
	}
}

// speculates reports whether this request may carry the Speculation-Rules
// header. Only a GET of a page: a POST has already happened, and an asset is
// not a document a browser reads rules from.
func speculates(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.HasPrefix(r.URL.Path, assets.Prefix) || strings.HasPrefix(r.URL.Path, "/media/") {
		return false
	}
	for _, prefix := range unspeculated {
		if r.URL.Path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(r.URL.Path, prefix) {
			return false
		}
	}
	return true
}

func requestLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("request_id", web.RequestID(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.statusCode()),
			slog.Duration("took", time.Since(start)),
		)
	})
}

func recoverPanic(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				// net/http's own recovery treats this as the handler's way to
				// abort the response; turning it into a 500 would send a body.
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				log.Error("panic serving request",
					"request_id", web.RequestID(r.Context()),
					"panic", v,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)
				// A handler that already sent its status cannot change it, and a
				// second body would be appended to a response that said 200.
				if rec.status != 0 {
					return
				}
				// i18n-exempt: the request has just panicked and the locale
				// middleware is one of the things that could have done it, so
				// both languages go in the literal rather than a lookup.
				http.Error(rec, "500 內部錯誤 / Internal error",
					http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// statusRecorder remembers the status a handler sent. Flush and Hijack reach
// the real writer through Unwrap, which [http.ResponseController] follows.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status != 0 {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		s.ResponseWriter.WriteHeader(code)
		return
	}
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// statusCode reports the status sent, treating a handler that wrote nothing as
// the 200 net/http sends on its behalf.
func (s *statusRecorder) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

var slugFormat = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// productFormSlug is the product a refused review or question form re-rendered
// under: those POST-only addresses have no page of their own to return to.
func productFormSlug(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/p/")
	if !ok {
		return "", false
	}
	for _, form := range []string{"/reviews", "/questions"} {
		if slug, ok := strings.CutSuffix(rest, form); ok && slugFormat.MatchString(slug) {
			return slug, true
		}
	}
	return "", false
}

// localeReturnPath is where a language switch sends the visitor back: the page
// they are on, query string included, so a search term, a filter or a chosen
// option survives the switch. The switch's handler still refuses anything but a
// same-site path.
func localeReturnPath(r *http.Request) string {
	if slug, ok := productFormSlug(r.URL.Path); ok {
		return "/p/" + slug
	}
	return r.URL.RequestURI()
}

func withLocale(next http.Handler, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.Detect(r, secure)
		// Vary, or a shared cache serves an English visitor the Chinese copy of
		// a page somebody else asked for.
		w.Header().Add("Vary", "Accept-Language, Cookie")
		ctx := i18n.WithLocale(r.Context(), l)
		// The path and query, so the language switch can send the visitor back.
		ctx = web.WithRequestPath(ctx, localeReturnPath(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bannerFreePrefixes are the paths that never show a promotion. The probes are
// here so no orchestrator health check makes a database round trip.
var bannerFreePrefixes = []string{
	"/checkout", "/cart", "/orders", "/account", "/admin",
	"/signin", "/register", "/forgot", "/reset", "/webhooks", "/media", "/static",
	"/healthz", "/readyz", "/favicon.ico",
}

// withBanner attaches the promotional strip to storefront requests. Not cached,
// because a shop that switches a promotion off expects it gone.
func withBanner(next http.Handler, store *home.Store, log *slog.Logger, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !storefrontPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		banner, err := store.Banner(r.Context(), home.ReadDismissal(r, secure))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // the caller left; there is nobody to serve
			}
			// Not fatal: an absent banner renders as nothing at all.
			log.ErrorContext(r.Context(), "read promo banner", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(layouts.WithBanner(r.Context(), banner)))
	})
}

// navFreePrefixes are the paths whose header carries no category row: the ones
// that render no storefront header at all, plus /admin, which has its own.
// Deliberately NOT bannerFreePrefixes — excluding a promotion from the checkout
// is a conversion decision, while the cart, the account pages and the sign-in
// form all render the site header and need its categories.
//
// The store map's return route is here for a different reason: it is an
// unauthenticated cross-site POST anybody can send, it renders no header at
// all, and leaving it a nav path would spend a category query on every one.
var navFreePrefixes = []string{
	"/admin", "/webhooks", "/media", "/static", "/healthz", "/readyz", "/favicon.ico",
	cart.PickupReturnPath,
}

// statelessPrefixes belong to no visitor: goen's own bytes, probes and the
// provider callback. The locale, signed-in user and cart badge are unused on
// these routes, so running their middleware would spend database round trips
// and put Vary: Cookie on immutable assets only to discard every result.
//
// This routing filter stays inside the ordinary middleware chain. In
// particular, /media serves attacker-supplied bytes and must retain nosniff,
// the CSP, request IDs, logging and panic recovery rather than being mounted on
// a second mux where one of those protections can drift.
//
// Every entry is also in navFreePrefixes and bannerFreePrefixes. /admin is
// deliberately absent because RequireStaff reads the user Authenticate puts
// on the context.
var statelessPrefixes = []string{
	"/static", "/media", "/healthz", "/readyz", "/webhooks", "/favicon.ico",
}

func statelessPath(path string) bool {
	for _, prefix := range statelessPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// onlyVisitorPaths applies mw where the route has a visitor and bypasses it on
// stateless routes. URL ownership belongs here rather than in account or cart:
// those feature packages should not know the application's route table.
func onlyVisitorPaths(mw func(http.Handler) http.Handler, next http.Handler) http.Handler {
	wrapped := mw(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statelessPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		wrapped.ServeHTTP(w, r)
	})
}

// withTopNav loads the header's category row. It runs inside withLocale,
// because the names it reads are localized and the locale has to be on the
// context before the query does. A failure degrades to a nav with no links.
//
// Every method, not just GET: a rejected form re-renders its own page at 422,
// which is exactly when a visitor is most likely to navigate away.
func withTopNav(next http.Handler, store *home.Store, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !navPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		items, err := store.Nav(r.Context())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return // the caller left; there is nobody to serve
			}
			log.ErrorContext(r.Context(), "read nav categories", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(layouts.WithTopNav(r.Context(), items)))
	})
}

// withStaffEntrance tells the chrome whether this visitor may reach the back
// office.
//
// It reads the user Authenticate has already put on the context, and it is
// middleware rather than a Page field for the reason the cart badge is — a
// chrome fact each handler has to remember to fill is one that goes unfilled.
//
// navPath is the predicate because it asks exactly the right question: those
// are the paths whose header carries the storefront's own navigation. /admin is
// not one of them, so the back office does not offer an entrance to itself
// beside the adminNav row that already leads there.
func withStaffEntrance(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := account.FromContext(r.Context())
		if !ok || !u.IsStaff() || !navPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(layouts.WithStaff(r.Context(), true)))
	})
}

// unstoredPrefixes own pages that carry one visitor's data or a secret whoever
// asks for them: the account, an order and the link that opens it, the cart and
// the checkout, the back office, and every page a mailed token opens. /returns
// and /payment are policy pages and stay cacheable; an order's own return and
// pay pages live under /orders.
var unstoredPrefixes = []string{
	"/account", "/admin", "/orders", "/cart", "/checkout",
	"/signin", "/register", "/forgot", "/reset", "/verify", "/newsletter", "/auth",
}

// withNoStore keeps every cache, the browser's back/forward cache included,
// from holding a response that belongs to one visitor. Otherwise the Back
// button on a shared computer shows the next person an account page, a
// customer list or a second-factor seed after the owner has signed out.
//
// It runs inside Authenticate, because anything rendered for a signed-in
// visitor is theirs. An anonymous visitor's catalogue page is left alone, so
// it still returns instantly with the Back button. The answer to a write is
// never stored either, because a refused form comes back with what was typed.
func withNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unstored(r) {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func unstored(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return true
	}
	if _, signedIn := account.FromContext(r.Context()); signedIn {
		return true
	}
	for _, prefix := range unstoredPrefixes {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return true
		}
	}
	return false
}

// navPath reports whether a path renders the storefront header.
func navPath(path string) bool {
	for _, prefix := range navFreePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}

func storefrontPath(path string) bool {
	for _, prefix := range bannerFreePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
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

// withSiteOrigin puts the configured origin and the request's path on every
// request's context, for the chrome's absolute URLs. A base URL that is not an origin adds nothing.
func withSiteOrigin(next http.Handler, baseURL string) http.Handler {
	origin, _, ok := web.SiteOrigin(baseURL)
	if !ok {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := layouts.WithRequestPath(layouts.WithSiteOrigin(r.Context(), origin), r.URL.EscapedPath())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
