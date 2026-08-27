package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/health"
	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/loyalty"
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
	"github.com/koopa0/goen/internal/warranty"
	"github.com/koopa0/goen/internal/web"
)

// contentSecurityPolicy is strict: goen renders no inline script and no inline
// style, and the two font hosts are the only third parties.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	// Browsers have applied form-action to the redirect a submission lands on,
	// and goen answers the payment form with a 303 to checkout.stripe.com.
	"form-action 'self' https://checkout.stripe.com; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// RouterConfig is what the router needs from configuration.
type RouterConfig struct {
	BaseURL string
	// SecureCookies selects the __Host- cookie prefix.
	SecureCookies bool
	// TOTPKey is parsed key material from twofactor.ParseKey; nil disables
	// enrolment. The type keeps raw configuration out of the cipher.
	TOTPKey []byte
	// Invoices issues uniform invoices, or is disabled and renders no controls.
	Invoices *invoice.Gateway
	// Google signs customers in, or is disabled and 404s its two routes.
	Google *account.Google
}

func newRouter(pool, adminPool *pgxpool.Pool, gateway *payment.Gateway, refunder admin.Refunder, cfg *RouterConfig, log *slog.Logger) http.Handler {
	baseURL, secureCookies, totpKey := cfg.BaseURL, cfg.SecureCookies, cfg.TOTPKey
	authLimit := ratelimit.New(ratelimit.Config{
		Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536,
	})
	catalogue := catalog.NewStore(pool)
	browse := catalog.NewHandler(catalogue, log)
	pages := site.NewHandler(log, baseURL, catalogue, site.NewStore(pool), secureCookies)
	storefront := home.NewHandler(home.NewStore(pool), log, secureCookies)
	probes := health.NewHandler(log,
		health.Dependency{Name: "storefront", DB: pool},
		health.Dependency{Name: "admin", DB: adminPool},
	)
	images := media.NewHandler(media.NewStore(pool), log)
	contactLimit := ratelimit.New(ratelimit.Config{
		Every: 5 * time.Minute, Burst: 5, TTL: time.Hour, MaxKeys: 65_536,
	})
	messages := contact.NewHandler(contact.NewStore(pool), contactLimit, log)
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
	items := product.NewHandler(product.NewStore(pool), log, baseURL)
	// Half of the order-lookup credential is a guessable order number, so
	// unlimited asking makes the endpoint an oracle for the other half.
	findLimit := ratelimit.New(ratelimit.Config{
		Every: 6 * time.Minute, Burst: 10, TTL: time.Hour, MaxKeys: 65_536,
	})
	basketStore := cart.NewStore(pool)
	basket := cart.NewHandler(basketStore, log, secureCookies, findLimit, sessionCloser(gateway))
	customers := account.NewHandler(account.NewStore(pool), basket, log, secureCookies, cfg.Google)
	// The second factor runs on the ADMIN pool. On the storefront pool `store`
	// would need write on staff_totp_credentials and users.role, so any slip
	// reachable from a product page escalates to admin.
	factorStore := twofactor.NewStore(adminPool, totpKey)
	factors := twofactor.NewHandler(factorStore, log, secureCookies)
	var stepUp func(*http.Request) (bool, error)
	if factorStore.Enabled() {
		stepUp = factors.StepUp
	}
	var invoices admin.Invoicer
	if cfg.Invoices.Enabled() {
		invoices = invoice.NewStore(adminPool, cfg.Invoices)
	}
	back := admin.NewHandler(admin.NewStore(adminPool, refunder, invoices),
		media.NewHandler(media.NewStore(adminPool), log),
		outbox.NewStore(adminPool, log), newsletter.NewStore(adminPool), log, stepUp,
		sessionCloser(gateway))
	// basketStore answers the order-access question for all three packages.
	till := payment.NewHandler(payment.NewStore(pool), gateway, basketStore, log, secureCookies)
	sendbacks := returns.NewHandler(returns.NewStore(pool), basketStore, log, secureCookies)

	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, staticAssetHandler(assets.Handler()))

	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

	mux.HandleFunc("GET /{$}", storefront.Home)
	// The digest in the path is the only authorisation an image has, and it is
	// unguessable by construction.
	mux.HandleFunc("GET /media/{digest}", images.Serve)
	mux.HandleFunc("GET /media/{digest}/{width}", images.Serve)
	mux.HandleFunc("GET /sitemap.xml", pages.Sitemap)
	mux.HandleFunc("GET /robots.txt", pages.Robots)
	mux.HandleFunc("POST /promo/dismiss", storefront.Dismiss)
	mux.HandleFunc("POST /locale", pages.SetLocale)
	mux.HandleFunc("GET /faq", pages.FAQ)
	mux.HandleFunc("GET /shipping", pages.Shipping)
	mux.HandleFunc("GET /returns", pages.Policy)
	mux.HandleFunc("GET /payment", pages.Policy)
	mux.HandleFunc("GET /warranty", pages.Policy)
	mux.HandleFunc("GET /privacy", pages.Policy)
	mux.HandleFunc("GET /terms", pages.Policy)
	mux.HandleFunc("GET /about", pages.About)
	mux.HandleFunc("GET /contact", messages.Page)
	mux.HandleFunc("POST /contact", messages.Submit)
	mux.HandleFunc("POST /newsletter", ratelimit.Guard(signupLimit, log, signups.Submit))
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
	mux.HandleFunc("POST /p/{slug}/notify", items.Notify)
	mux.HandleFunc("POST /p/{slug}/questions", items.Ask)
	mux.HandleFunc("GET /cart", basket.Page)
	mux.HandleFunc("POST /cart/items", basket.AddItem)
	mux.HandleFunc("POST /cart/items/update", basket.UpdateItem)
	mux.HandleFunc("GET /checkout", basket.Checkout)
	mux.HandleFunc("POST /checkout", basket.PlaceOrder)
	mux.HandleFunc("GET /orders/find", basket.FindOrderPage)
	mux.HandleFunc("POST /orders/find", ratelimit.Guard(findLimit, log, basket.FindOrder))
	mux.HandleFunc("GET /orders/{number}", basket.OrderPage)
	mux.HandleFunc("POST /orders/{number}/cancel", basket.CancelOrder)
	mux.HandleFunc("POST /orders/{number}/reorder", basket.ReorderItems)
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
	mux.HandleFunc("POST /signin", ratelimit.Guard(authLimit, log, customers.SignIn))
	mux.HandleFunc("GET /forgot", customers.ForgotPage)
	mux.HandleFunc("POST /forgot", ratelimit.Guard(authLimit, log, customers.Forgot))
	mux.HandleFunc("GET /reset", customers.ResetPage)
	mux.HandleFunc("POST /reset", ratelimit.Guard(authLimit, log, customers.Reset))
	// Open to a signed-OUT visitor on purpose: the token is the proof, not the
	// session, and the link is followed on whatever device the mail is on.
	mux.HandleFunc("GET /verify", customers.VerifyPage)
	mux.HandleFunc("POST /verify", ratelimit.Guard(authLimit, log, customers.Verify))
	mux.HandleFunc("GET /register", customers.RegisterPage)
	mux.HandleFunc("POST /register", ratelimit.Guard(authLimit, log, customers.Register))
	mux.HandleFunc("POST /signout", customers.SignOut)
	mux.HandleFunc("GET /account", customers.RequireUser(customers.Overview))
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
	mux.HandleFunc("GET /admin", back.RequireStaff(back.Dashboard))
	mux.HandleFunc("GET /admin/orders", back.RequireStaff(back.Orders))
	mux.HandleFunc("GET /admin/orders/{number}", back.RequireStaff(back.Order))
	mux.HandleFunc("POST /admin/orders/{number}/status", back.RequireStaff(back.AdvanceOrder))
	mux.HandleFunc("POST /admin/orders/{number}/ship", back.RequireStaff(back.Ship))
	mux.HandleFunc("POST /admin/orders/{number}/note", back.RequireStaff(back.StaffNote))
	mux.HandleFunc("POST /admin/orders/{number}/delivery", back.RequireStaff(back.CorrectDelivery))
	mux.HandleFunc("GET /admin/products", back.RequireStaff(back.Products))
	mux.HandleFunc("POST /admin/products", back.RequireStaff(back.CreateProduct))
	mux.HandleFunc("GET /admin/products/new", back.RequireStaff(back.NewProduct))
	mux.HandleFunc("GET /admin/products/{slug}", back.RequireStaff(back.EditProduct))
	mux.HandleFunc("POST /admin/products/{slug}", back.RequireStaff(back.UpdateProduct))
	mux.HandleFunc("POST /admin/products/{slug}/status", back.RequireStaff(back.PublishProduct))
	mux.HandleFunc("POST /admin/products/{slug}/variants", back.RequireStaff(back.AddVariant))
	mux.HandleFunc("GET /admin/stock", back.RequireStaff(back.Variants))
	mux.HandleFunc("GET /admin/stock/{sku}", back.RequireStaff(back.Movements))
	mux.HandleFunc("POST /admin/stock/adjust", back.RequireStaff(back.AdjustStock))
	mux.HandleFunc("POST /admin/stock/receive", back.RequireStaff(back.ReceiveStock))
	mux.HandleFunc("POST /admin/stock/active", back.RequireStaff(back.SetVariantActive))
	mux.HandleFunc("POST /admin/stock/price", back.RequireStaff(back.SetVariantPrice))
	mux.HandleFunc("GET /admin/returns", back.RequireStaff(back.Returns))
	mux.HandleFunc("POST /admin/returns/{id}/decide", back.RequireStaff(back.Decide))
	mux.HandleFunc("POST /admin/returns/{id}/inspect", back.RequireStaff(back.Inspect))
	mux.HandleFunc("POST /admin/returns/{id}/complete", back.RequireStaff(back.Complete))
	mux.HandleFunc("POST /admin/orders/{number}/invoice", back.RequireStaff(back.IssueInvoice))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/void", back.RequireStaff(back.VoidInvoice))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/allowance", back.RequireStaff(back.AllowInvoice))
	mux.HandleFunc("POST /admin/products/{slug}/options", back.RequireStaff(back.AddOption))
	mux.HandleFunc("POST /admin/products/{slug}/options/values", back.RequireStaff(back.AddOptionValue))
	mux.HandleFunc("POST /admin/products/{slug}/specs", back.RequireStaff(back.AddSpec))
	mux.HandleFunc("POST /admin/products/{slug}/specs/remove", back.RequireStaff(back.RemoveSpec))
	mux.HandleFunc("POST /admin/products/{slug}/images", back.RequireStaff(back.UploadImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/reuse", back.RequireStaff(back.ReuseImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/remove", back.RequireStaff(back.RemoveImage))
	mux.HandleFunc("GET /admin/tiers", back.RequireStaff(back.Tiers))
	mux.HandleFunc("POST /admin/tiers", back.RequireStaff(back.CreateTier))
	mux.HandleFunc("POST /admin/tiers/delete", back.RequireStaff(back.DeleteTier))
	mux.HandleFunc("GET /admin/shipping", back.RequireStaff(back.Shipping))
	mux.HandleFunc("POST /admin/shipping/version", back.RequireStaff(back.PublishShippingVersion))
	mux.HandleFunc("POST /admin/shipping/surcharge", back.RequireStaff(back.SetZoneSurcharge))
	mux.HandleFunc("POST /admin/shipping/method", back.RequireStaff(back.CreateShippingMethod))
	mux.HandleFunc("POST /admin/shipping/method/{id}/active", back.RequireStaff(back.SetShippingMethodActive))
	mux.HandleFunc("POST /admin/shipping/zone", back.RequireStaff(back.CreateShippingZone))
	mux.HandleFunc("POST /admin/shipping/zone/{id}/prefixes", back.RequireStaff(back.SetZonePrefixes))
	mux.HandleFunc("POST /admin/shipping/zone/{id}/delete", back.RequireStaff(back.DeleteShippingZone))
	// RequireAdmin and not RequireStaff, which accepts `staff` as well: these
	// four promote, revoke, and strip an admin's second factor, and the listing
	// names who has none yet.
	mux.HandleFunc("GET /admin/staff", back.RequireAdmin(factors.Staff))
	mux.HandleFunc("POST /admin/staff", back.RequireAdmin(factors.AddStaff))
	mux.HandleFunc("POST /admin/staff/revoke", back.RequireAdmin(factors.RevokeStaff))
	mux.HandleFunc("POST /admin/staff/factor", back.RequireAdmin(factors.RemoveFactor))
	mux.HandleFunc("GET /admin/verify", back.StaffOnly(factors.Challenge))
	mux.HandleFunc("POST /admin/verify", back.StaffOnly(factors.Verify))
	mux.HandleFunc("POST /admin/verify/enrol", back.StaffOnly(factors.Enrol))
	mux.HandleFunc("POST /admin/verify/confirm", back.StaffOnly(factors.Confirm))
	mux.HandleFunc("GET /admin/faq", back.RequireStaff(back.FAQ))
	mux.HandleFunc("POST /admin/faq", back.RequireStaff(back.CreateFAQEntry))
	mux.HandleFunc("POST /admin/faq/{id}", back.RequireStaff(back.EditFAQEntry))
	mux.HandleFunc("GET /admin/warranty", back.RequireStaff(back.Warranties))
	mux.HandleFunc("GET /admin/customers", back.RequireStaff(back.Customers))
	mux.HandleFunc("GET /admin/customers/{id}", back.RequireStaff(back.Customer))
	mux.HandleFunc("GET /admin/messages", back.RequireStaff(back.Messages))
	mux.HandleFunc("GET /admin/newsletter", back.RequireStaff(back.Newsletter))
	mux.HandleFunc("POST /admin/newsletter", back.RequireStaff(back.ComposeNewsletter))
	mux.HandleFunc("POST /admin/newsletter/{id}/send", back.RequireStaff(back.SendNewsletter))
	mux.HandleFunc("POST /admin/messages/handle", back.RequireStaff(back.HandleMessage))
	mux.HandleFunc("POST /admin/messages/reopen", back.RequireStaff(back.ReopenMessage))
	mux.HandleFunc("GET /admin/reviews", back.RequireStaff(back.Reviews))
	mux.HandleFunc("POST /admin/reviews/hide", back.RequireStaff(back.HideReview))
	mux.HandleFunc("POST /admin/reviews/show", back.RequireStaff(back.ShowReview))
	mux.HandleFunc("GET /admin/questions", back.RequireStaff(back.Questions))
	mux.HandleFunc("POST /admin/questions/{id}", back.RequireStaff(back.AnswerQuestion))
	mux.HandleFunc("GET /admin/health", back.RequireStaff(back.Health))
	mux.HandleFunc("POST /admin/health/reconcile", back.RequireStaff(back.ReconcilePayment))
	mux.HandleFunc("GET /admin/reports", back.RequireStaff(back.Reports))
	mux.HandleFunc("GET /admin/taxonomy", back.RequireStaff(back.Taxonomy))
	mux.HandleFunc("POST /admin/taxonomy/{kind}", back.RequireStaff(back.CreateTaxon))
	mux.HandleFunc("POST /admin/taxonomy/{kind}/{slug}", back.RequireStaff(back.EditTaxon))
	mux.HandleFunc("GET /admin/home", back.RequireStaff(back.HomeContent))
	mux.HandleFunc("POST /admin/home", back.RequireStaff(back.CreateHeroSlide))
	mux.HandleFunc("POST /admin/home/banner", back.RequireStaff(back.CreateBanner))
	mux.HandleFunc("POST /admin/home/banner/{id}/active", back.RequireStaff(back.SetBannerActive))
	mux.HandleFunc("POST /admin/home/{id}/active", back.RequireStaff(back.SetHeroSlideActive))
	mux.HandleFunc("POST /admin/home/{id}/promote", back.RequireStaff(back.PromoteHeroSlide))
	mux.HandleFunc("GET /admin/audit", back.RequireStaff(back.Audit))
	mux.HandleFunc("GET /admin/campaigns", back.RequireStaff(back.Campaigns))
	mux.HandleFunc("POST /admin/campaigns", back.RequireStaff(back.CreateCampaign))
	mux.HandleFunc("GET /admin/campaigns/{slug}", back.RequireStaff(back.EditCampaign))
	mux.HandleFunc("POST /admin/campaigns/{slug}/products", back.RequireStaff(back.FeatureProduct))
	mux.HandleFunc("POST /admin/campaigns/{slug}/active", back.RequireStaff(back.SetCampaignActive))
	mux.HandleFunc("GET /admin/coupons", back.RequireStaff(back.Coupons))
	mux.HandleFunc("POST /admin/coupons", back.RequireStaff(back.CreateCoupon))
	mux.HandleFunc("POST /admin/coupons/{code}/active", back.RequireStaff(back.SetCouponActive))
	mux.HandleFunc("GET /admin/credit", back.RequireStaff(back.Credit))
	mux.HandleFunc("POST /admin/credit", back.RequireStaff(back.GrantCredit))

	mux.HandleFunc("GET /", pages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse of
	// this order. The chrome middleware fills what every page shows, and the
	// locale is on the context before any handler or template reads it.
	var handler http.Handler = mux
	handler = withBanner(handler, home.NewStore(pool), log, secureCookies)
	handler = withTopNav(handler, home.NewStore(pool), log)
	handler = onlyVisitorPaths(func(next http.Handler) http.Handler {
		return withLocale(next, secureCookies)
	}, handler)
	handler = onlyVisitorPaths(basket.WithCount, handler)
	handler = onlyVisitorPaths(customers.Authenticate, handler)
	handler = crossOriginProtection(handler)
	handler = securityHeaders(handler)
	handler = web.Compress(handler)
	handler = requestLog(handler, log)
	handler = withRequestID(handler)
	return recoverPanic(handler, log)
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

// crossOriginProtection rejects cross-site form posts using the browser's own
// Sec-Fetch-Site signal, which is why goen's forms carry no CSRF token.
func crossOriginProtection(next http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(next)
}

type requestIDKey struct{}

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
		// Two keys: this package's logging middleware reads the local one, and a
		// feature reads internal/web's when it stamps an audit row.
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(web.WithRequestID(ctx, id)))
	})
}

// validRequestID accepts the shape an id may take.
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

// requestID returns the identifier attached to ctx, or "" outside the chain.
func requestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("request_id", requestID(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.statusCode()),
			slog.Duration("took", time.Since(start)),
		)
	})
}

func recoverPanic(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic serving request",
					"request_id", requestID(r.Context()),
					"panic", v,
					"method", r.Method,
					"path", r.URL.Path,
				)
				// i18n-exempt: the request has just panicked and the locale
				// middleware is one of the things that could have done it, so
				// both languages go in the literal rather than a lookup.
				http.Error(w, "500 內部錯誤 / Internal error",
					http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
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

// withLocale attaches the request's language to its context.
func withLocale(next http.Handler, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := i18n.Detect(r, secure)
		// Vary, or a shared cache serves an English visitor the Chinese copy of
		// a page somebody else asked for.
		w.Header().Add("Vary", "Accept-Language, Cookie")
		ctx := i18n.WithLocale(r.Context(), l)
		// The path, so the language switch can send the visitor back. RawQuery
		// is dropped: it would carry a search term into a redirect target.
		ctx = web.WithRequestPath(ctx, r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bannerFreePrefixes are the paths that never show a promotion. The probes are
// here so no orchestrator health check makes a database round trip.
var bannerFreePrefixes = []string{
	"/checkout", "/cart", "/orders", "/account", "/admin",
	"/signin", "/register", "/forgot", "/reset", "/webhooks", "/media", "/static",
	"/healthz", "/readyz",
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
//
// Deliberately NOT bannerFreePrefixes. Excluding a promotion from the checkout
// is a conversion decision; excluding NAVIGATION is not, and the cart, the
// account pages and the sign-in form all render the site header — with an empty
// category row, on the chrome CLAUDE.md calls the most-read on the site.
var navFreePrefixes = []string{
	"/admin", "/webhooks", "/media", "/static", "/healthz", "/readyz",
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
// Every entry is also in navFreePrefixes and bannerFreePrefixes;
// TestNothingStatelessRendersChrome holds that containment. /admin is
// deliberately absent because RequireStaff reads the user Authenticate puts
// on the context.
var statelessPrefixes = []string{
	"/static", "/media", "/healthz", "/readyz", "/webhooks",
}

// statelessPath reports whether a path carries no per-visitor state.
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
			log.ErrorContext(r.Context(), "read nav categories", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(layouts.WithTopNav(r.Context(), items)))
	})
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

// storefrontPath reports whether a path is somewhere a promotion belongs.
func storefrontPath(path string) bool {
	for _, prefix := range bannerFreePrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}

// sessionCloser hands the gateway to the two cancel doors, or nothing at all.
// It returns an explicitly nil interface rather than a nil *Gateway, because a
// typed nil in an interface is non-nil and each handler's nil check would miss.
func sessionCloser(g *payment.Gateway) cart.SessionCloser {
	if g == nil || !g.Enabled() {
		return nil
	}
	return g
}
