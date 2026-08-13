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

// contentSecurityPolicy is deliberately strict: goen renders no inline script
// and no inline style, so neither needs an allowance. The two font hosts are
// the only third parties, and dropping them is the follow-up that self-hosting
// the faces would close.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	// Stripe's hosted checkout is named here because the payment form posts to
	// goen and goen answers 303 to checkout.stripe.com. Chrome and Safari have
	// both, at points in their history, applied form-action to the redirect a
	// form submission lands on — leaving it off makes paying work or not work
	// depending on the browser.
	"form-action 'self' https://checkout.stripe.com; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// RouterConfig is what the router needs from configuration.
//
// A struct because the parameter list had reached eight, and three of them were
// strings — at that width a call site says nothing about which argument is
// which, which is the case the style guide names for a named type.
type RouterConfig struct {
	// BaseURL is the origin absolute URLs are built from: Stripe's return
	// addresses, the sitemap, and JSON-LD.
	BaseURL string
	// SecureCookies selects the __Host- cookie prefix. False only in
	// development, where a __Host- cookie would never come back over http://.
	SecureCookies bool
	// TOTPKey encrypts stored second-factor secrets. Empty disables enrolment
	// rather than storing one in the clear.
	TOTPKey string
	// Invoices issues 統一發票 through the 加值中心. A disabled one renders no
	// controls and says why, the way an absent Stripe key does — never a button
	// that can only fail.
	Invoices *invoice.Gateway
	// Google signs customers in through their Google account. A disabled client
	// renders no button and 404s its two routes, so a deployment without
	// credentials looks like one that never offered it.
	Google *account.Google
}

func newRouter(pool, adminPool *pgxpool.Pool, gateway *payment.Gateway, refunder admin.Refunder, cfg *RouterConfig, log *slog.Logger) http.Handler {
	baseURL, secureCookies, totpKey := cfg.BaseURL, cfg.SecureCookies, cfg.TOTPKey
	// One catalogue store, shared: the sitemap reads the same thing the
	// listing does, and two stores over one pool is two of everything for no
	// reason a reader could name.
	// Twenty attempts, one back every three seconds, per IP. Generous enough
	// that a household behind one address never meets it and tight enough that
	// a script cannot hold the process at 64 MiB a request.
	authLimit := ratelimit.New(ratelimit.Config{
		Every: 3 * time.Second, Burst: 20, TTL: time.Hour,
	})
	catalogue := catalog.NewStore(pool)
	browse := catalog.NewHandler(catalogue, log)
	pages := site.NewHandler(log, baseURL, catalogue, site.NewStore(pool), secureCookies)
	storefront := home.NewHandler(home.NewStore(pool), log, secureCookies)
	// Both pools, because a process that can serve the storefront and not the
	// back office is not ready — see health.NewHandler.
	probes := health.NewHandler(log,
		health.Dependency{Name: "storefront", DB: pool},
		health.Dependency{Name: "admin", DB: adminPool},
	)
	images := media.NewHandler(media.NewStore(pool), log)
	// Writing in is a slower act than signing in, and the bound says so: a
	// handful of messages an hour from one address is far more than anybody with
	// something to say needs, and far less than a script needs to bury the
	// customer who wrote yesterday under ten thousand rows.
	contactLimit := ratelimit.New(ratelimit.Config{
		Every: 5 * time.Minute, Burst: 5, TTL: time.Hour,
	})
	messages := contact.NewHandler(contact.NewStore(pool), contactLimit, log)
	// A separate limiter from authLimit, and much tighter. This one is not
	// defending argon2 — it is defending somebody else's mailbox, which one
	// request per address every ten minutes is generous for.
	signupLimit := ratelimit.New(ratelimit.Config{
		Every: 10 * time.Minute, Burst: 2, TTL: time.Hour,
	})
	// TWO stores over two pools, because the two halves of this feature are two
	// roles. The customer side — subscribe, confirm, unsubscribe — writes
	// newsletter_subscribers as `store`. Composing and SENDING writes
	// newsletter_issues and an audit row, and `store` holds neither: the send
	// belongs to the back office and runs as `admin`.
	//
	// One store over the storefront pool would have failed at the first compose,
	// and only in production — every test connects as the owner, who is subject to
	// no missing grant. That is the trap CLAUDE.md records from committed_orders.
	signups := newsletter.NewHandler(newsletter.NewStore(pool), signupLimit, log)
	cover := warranty.NewHandler(warranty.NewStore(pool), log)
	points := loyalty.NewHandler(loyalty.NewStore(pool), log)
	items := product.NewHandler(product.NewStore(pool), log, baseURL)
	// Ten lookups an hour per address, one back every six minutes. Half of that
	// credential is an order number off a per-day counter, so the endpoint is an
	// oracle for the other half if it can be asked without limit — and the other
	// half is somebody's delivery address.
	findLimit := ratelimit.New(ratelimit.Config{
		Every: 6 * time.Minute, Burst: 10, TTL: time.Hour,
	})
	// The cart STORE is named, because it is what answers the order-access question
	// and what the payment and return handlers depend on. The handler is the HTTP
	// face of it.
	basketStore := cart.NewStore(pool)
	// Cancelling an order has to close the checkout it may have left open at
	// Stripe, so the gateway reaches BOTH cancel doors — the customer's own and
	// the back office's — as a one-method interface each package defines for
	// itself. Nil when there is no Stripe key; see sessionCloser.
	basket := cart.NewHandler(basketStore, log, secureCookies, findLimit, sessionCloser(gateway))
	customers := account.NewHandler(account.NewStore(pool), basket, log, secureCookies, cfg.Google)
	// The second factor, on the ADMIN pool — every route it serves is a
	// back-office route, and every table it writes is a back-office table.
	//
	// It used to run on the STOREFRONT pool, and that one line was what put the
	// whole /admin story behind the role that serves anonymous product pages.
	// `store` needed write on staff_totp_credentials to enrol, and write on
	// users.role to manage colleagues — so any injection or logic slip reachable
	// from a storefront handler escalated to admin in three statements: set your
	// own role, delete the target's second factor, sign in. The privilege model
	// was sound and the WIRING handed the keys around it.
	//
	// The split is the one internal/newsletter already makes for the same reason:
	// which pool a store runs on is a statement about who may perform its writes.
	factorStore := twofactor.NewStore(adminPool, totpKey)
	factors := twofactor.NewHandler(factorStore, log, secureCookies)
	// A deployment with no key gets a nil step-up function and a back office that
	// behaves as it did before — the same shape as Stripe: the feature is off,
	// loudly, rather than half on.
	var stepUp func(*http.Request) (bool, error)
	if factorStore.Enabled() {
		stepUp = factors.StepUp
	}
	// The 加值中心, or nil when none is configured — which renders no invoice
	// controls and says why, rather than a button that can only fail.
	var invoices admin.Invoicer
	if cfg.Invoices.Enabled() {
		invoices = invoice.NewStore(adminPool, cfg.Invoices)
	}
	back := admin.NewHandler(admin.NewStore(adminPool, refunder, invoices),
		media.NewHandler(media.NewStore(adminPool), log),
		// The queue, read-only from here: /admin/health lists which messages
		// have given up. The worker that DELIVERS them is main's, on the
		// storefront pool.
		outbox.NewStore(adminPool, log), newsletter.NewStore(adminPool), log, stepUp,
		sessionCloser(gateway))
	// basket is passed as the ORDER ACCESS check to both: whether this browser holds
	// a token for the order it is asking about is one question with one answer, and
	// three packages each deciding it is three places for it to be wrong.
	till := payment.NewHandler(payment.NewStore(pool), gateway, basketStore, log, secureCookies)
	sendbacks := returns.NewHandler(returns.NewStore(pool), basketStore, log, secureCookies)

	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, assets.Handler())

	// Probes come first because they must answer even when everything behind
	// them does not.
	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

	mux.HandleFunc("GET /{$}", storefront.Home)
	// Images are public and cacheable, so this sits with the static routes
	// rather than behind anything: the digest in the path is the only
	// authorisation there is, and it is unguessable by construction.
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
	// Both links open a page carrying a form; the POST is what writes. A GET
	// that confirmed or unsubscribed would be completed by every link scanner
	// that reads the mail before its owner does.
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
	// Before the {number} route: a literal path wins in net/http's mux either way,
	// but reading them in this order is how somebody checks that.
	mux.HandleFunc("GET /orders/find", basket.FindOrderPage)
	mux.HandleFunc("POST /orders/find", ratelimit.Guard(findLimit, log, basket.FindOrder))
	mux.HandleFunc("GET /orders/{number}", basket.OrderPage)
	mux.HandleFunc("POST /orders/{number}/cancel", basket.CancelOrder)
	mux.HandleFunc("POST /orders/{number}/reorder", basket.ReorderItems)
	mux.HandleFunc("GET /orders/{number}/pay", till.Page)
	mux.HandleFunc("POST /orders/{number}/pay", till.Start)
	mux.HandleFunc("GET /orders/{number}/return", sendbacks.Page)
	mux.HandleFunc("POST /orders/{number}/return", sendbacks.Submit)

	// Stripe posts here server-to-server. It carries no cookie and no Origin,
	// so the cross-origin middleware lets it through — a browser-fetch-metadata
	// check has nothing to check on a request no browser made. Its
	// authentication is the Stripe-Signature header, which the handler verifies
	// before the body means anything. TestWebhookSurvivesTheMiddlewareChain is
	// what keeps that true.
	mux.HandleFunc("POST /webhooks/stripe", till.Webhook)

	mux.HandleFunc("GET /signin", customers.SignInPage)
	// Per-IP, in front of the handler so it runs before argon2 does. 64 MiB a
	// hash means an unbounded sign-in endpoint is a memory exhaustion anybody
	// can trigger, which is a bigger problem than the credential stuffing this
	// also slows.
	//
	// One limiter shared by all three: they are the endpoints where a request
	// is expensive or reveals whether an account exists, and an attacker moving
	// between them should not get a fresh allowance for each.
	// Google sign-in. GET on both: the first writes only a short-lived cookie
	// and redirects, and the second is a redirect FROM GOOGLE whose method goen
	// does not choose. Nothing about an account changes until the callback has
	// matched the state it issued.
	mux.HandleFunc("GET /auth/google", customers.GoogleSignIn)
	mux.HandleFunc("GET /auth/google/callback", customers.GoogleCallback)
	mux.HandleFunc("POST /signin", ratelimit.Guard(authLimit, log, customers.SignIn))
	// The way back in when the password is gone. argon2 means nobody at the
	// shop can look one up, so without these four routes a forgotten password
	// is a permanently locked account.
	mux.HandleFunc("GET /forgot", customers.ForgotPage)
	mux.HandleFunc("POST /forgot", ratelimit.Guard(authLimit, log, customers.Forgot))
	mux.HandleFunc("GET /reset", customers.ResetPage)
	mux.HandleFunc("POST /reset", ratelimit.Guard(authLimit, log, customers.Reset))
	// Open to a signed-OUT visitor on purpose: somebody who changed their address
	// follows the link on whatever device their mail is on, which is often not the
	// browser they are signed in to. The token is the proof, not the session.
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
	// Also guarded: changing a password verifies the OLD one with argon2, so a
	// signed-in session is otherwise an unbounded supply of 64 MiB hashes.
	mux.HandleFunc("POST /account/email",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ChangeEmail)))
	mux.HandleFunc("POST /account/email/resend",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ResendVerification)))
	mux.HandleFunc("POST /account/password",
		ratelimit.Guard(authLimit, log, customers.RequireUser(customers.ChangePassword)))
	mux.HandleFunc("POST /account/erase", customers.RequireUser(customers.Erase))
	// Unlinking is a POST, because it writes. Refused when it is the only way
	// in: an account with no password and no identity is one nobody can reach.
	mux.HandleFunc("POST /account/google/unlink", customers.RequireUser(customers.UnlinkGoogle))

	// The back office. Every route is staff-only, and a signed-in customer gets
	// a 404 rather than a 403 — a 403 confirms that /admin is a real place.
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
	// 進貨, which is a different fact from a correction and had no door of its
	// own: the ledger's 'receipt' reason was posted by the dev seed and by
	// nothing a shop can reach.
	mux.HandleFunc("POST /admin/stock/receive", back.RequireStaff(back.ReceiveStock))
	mux.HandleFunc("POST /admin/stock/active", back.RequireStaff(back.SetVariantActive))
	mux.HandleFunc("POST /admin/stock/price", back.RequireStaff(back.SetVariantPrice))
	mux.HandleFunc("GET /admin/returns", back.RequireStaff(back.Returns))
	mux.HandleFunc("POST /admin/returns/{id}/decide", back.RequireStaff(back.Decide))
	// The tail a return used to have no door to: the parcel arrives, somebody
	// opens it, and the sellable units go back on the shelf through the ledger.
	mux.HandleFunc("POST /admin/returns/{id}/inspect", back.RequireStaff(back.Inspect))
	mux.HandleFunc("POST /admin/returns/{id}/complete", back.RequireStaff(back.Complete))
	// 統一發票. The preference has been collected at checkout since the day it
	// shipped, and until now nothing could act on it.
	mux.HandleFunc("POST /admin/orders/{number}/invoice", back.RequireStaff(back.IssueInvoice))
	mux.HandleFunc("POST /admin/orders/{number}/invoice/void", back.RequireStaff(back.VoidInvoice))
	mux.HandleFunc("POST /admin/products/{slug}/options", back.RequireStaff(back.AddOption))
	mux.HandleFunc("POST /admin/products/{slug}/options/values", back.RequireStaff(back.AddOptionValue))
	mux.HandleFunc("POST /admin/products/{slug}/specs", back.RequireStaff(back.AddSpec))
	mux.HandleFunc("POST /admin/products/{slug}/specs/remove", back.RequireStaff(back.RemoveSpec))
	mux.HandleFunc("POST /admin/products/{slug}/images", back.RequireStaff(back.UploadImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/reuse", back.RequireStaff(back.ReuseImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/remove", back.RequireStaff(back.RemoveImage))
	// NOT behind RequireStaff's second-factor gate — it is how the factor is
	// proved, so gating it on itself is a redirect loop. It still requires a
	// signed-in staff member, checked inside each handler.
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
	// ADMIN only, not staff. Who works here is not a staff job — these four
	// routes were gated on RequireStaff, which accepts `staff` as well, so any
	// staff member could POST their own address with role=admin and be promoted,
	// revoke a colleague, or strip an admin's second factor. The listing goes
	// too: it names who has no second factor yet, which its own handler calls a
	// map of where the back office is weakest.
	mux.HandleFunc("GET /admin/staff", back.RequireAdmin(factors.Staff))
	mux.HandleFunc("POST /admin/staff", back.RequireAdmin(factors.AddStaff))
	mux.HandleFunc("POST /admin/staff/revoke", back.RequireAdmin(factors.RevokeStaff))
	mux.HandleFunc("POST /admin/staff/factor", back.RequireAdmin(factors.RemoveFactor))
	mux.HandleFunc("GET /admin/verify", customers.RequireUser(factors.Challenge))
	mux.HandleFunc("POST /admin/verify", customers.RequireUser(factors.Verify))
	mux.HandleFunc("POST /admin/verify/enrol", customers.RequireUser(factors.Enrol))
	mux.HandleFunc("POST /admin/verify/confirm", customers.RequireUser(factors.Confirm))
	mux.HandleFunc("GET /admin/faq", back.RequireStaff(back.FAQ))
	mux.HandleFunc("POST /admin/faq", back.RequireStaff(back.CreateFAQEntry))
	mux.HandleFunc("POST /admin/faq/{id}", back.RequireStaff(back.EditFAQEntry))
	// The shop's half of warranty registration. The customer's half has existed
	// since the feature shipped; this side had nothing, so a claim arrived and
	// the only record of it was held by the person claiming.
	mux.HandleFunc("GET /admin/warranty", back.RequireStaff(back.Warranties))
	mux.HandleFunc("GET /admin/customers", back.RequireStaff(back.Customers))
	mux.HandleFunc("GET /admin/customers/{id}", back.RequireStaff(back.Customer))
	mux.HandleFunc("GET /admin/messages", back.RequireStaff(back.Messages))
	mux.HandleFunc("GET /admin/newsletter", back.RequireStaff(back.Newsletter))
	mux.HandleFunc("POST /admin/newsletter", back.RequireStaff(back.ComposeNewsletter))
	// The one irreversible button in the back office: ten thousand mailboxes
	// cannot be edited afterwards, so composing and sending are two forms.
	mux.HandleFunc("POST /admin/newsletter/{id}/send", back.RequireStaff(back.SendNewsletter))
	mux.HandleFunc("POST /admin/messages/handle", back.RequireStaff(back.HandleMessage))
	mux.HandleFunc("POST /admin/messages/reopen", back.RequireStaff(back.ReopenMessage))
	mux.HandleFunc("GET /admin/reviews", back.RequireStaff(back.Reviews))
	mux.HandleFunc("POST /admin/reviews/hide", back.RequireStaff(back.HideReview))
	mux.HandleFunc("POST /admin/reviews/show", back.RequireStaff(back.ShowReview))
	mux.HandleFunc("GET /admin/questions", back.RequireStaff(back.Questions))
	mux.HandleFunc("POST /admin/questions/{id}", back.RequireStaff(back.AnswerQuestion))
	mux.HandleFunc("GET /admin/health", back.RequireStaff(back.Health))
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

	// Everything the storefront does not serve yet, including the routes the
	// header and footer already link to.
	mux.HandleFunc("GET /", pages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse
	// of this order: recover, then tag with an id, then log, then the security
	// headers, then the cross-origin check. The id is attached before logging
	// so every line about one request carries the same one, and before the
	// recover handler unwinds so a panic is traceable to its request.
	var handler http.Handler = mux
	// Authentication sits INSIDE the security middleware and outside the mux, so
	// every handler can read the signed-in user and none of them has to look the
	// session up itself. It rejects nobody — a signed-out visitor is valid on
	// almost every page, and the pages that need an account say so themselves.
	// The cart badge. Inside the security middleware and outside the mux, for
	// the same reason authentication is: every page's chrome shows it, and no
	// handler should have to remember to fill it in.
	// The locale, outermost of the request-scoped middleware: every later
	// handler and every template reads it, and <html lang> is decided before
	// the first byte so a page never renders in one language and changes.
	// The promotional strip, decided by PATH.
	//
	// Which pages are "the storefront" is a fact about routing, so it is
	// decided here rather than by each handler remembering to ask for it — the
	// lesson layouts.Page.CartCount already taught: a field every handler must
	// fill is a field that goes unfilled.
	handler = withBanner(handler, home.NewStore(pool), log, secureCookies)
	handler = withTopNav(handler, home.NewStore(pool), log)
	handler = withLocale(handler, secureCookies)
	handler = basket.WithCount(handler)
	handler = customers.Authenticate(handler)
	handler = crossOriginProtection(handler)
	handler = securityHeaders(handler)
	handler = requestLog(handler, log)
	handler = withRequestID(handler)
	return recoverPanic(handler, log)
}

// crossOriginProtection rejects cross-site form posts using the browser's own
// Sec-Fetch-Site signal, which is why goen's forms carry no CSRF token.
func crossOriginProtection(next http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(next)
}

// requestIDKey is unexported so nothing outside this package can put a value
// under it, which is what keeps [RequestID] honest about where the id came
// from.
type requestIDKey struct{}

// withRequestID gives every request an identifier and echoes it back.
//
// A client-supplied X-Request-Id is honoured so a trace can span a proxy, but
// only when it looks like an id: an arbitrary header value would otherwise
// reach the logs, where an attacker-chosen newline could forge a log line.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		// Both: the local key is what this package's logging middleware reads,
		// and internal/web's is what a feature reads — the audit trail stamps
		// it onto every back-office row so a row and a log line can be put
		// beside each other.
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(web.WithRequestID(ctx, id)))
	})
}

// validRequestID accepts the shape an id may take: printable ASCII, bounded,
// and nothing that could break a log line.
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

// requestID returns the identifier attached to r, or "" outside the chain —
// which happens in a test that calls a handler directly.
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
				// The handler may already have written; this is best effort.
				//
				// i18n-exempt: the request has just panicked, and the locale
				// middleware is one of the things that could have done it — a
				// catalogue lookup here is a dependency the one path that must
				// never fail twice does not need. Both languages in the literal
				// instead, so neither reader gets a line they cannot read.
				http.Error(w, "500 內部錯誤 / Internal error",
					http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status a handler sent so the request log can
// report it.
//
// It implements only WriteHeader and Write. Flush and Hijack reach the real
// writer through Unwrap, which [http.ResponseController] follows, so wrapping
// does not remove a capability from anything downstream.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// statusCode reports the status sent, treating a handler that wrote nothing at
// all as the 200 net/http sends on its behalf.
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
		// Vary, because the same URL renders differently per language: without
		// it a shared cache serves an English visitor the Chinese copy of a
		// page somebody else asked for.
		w.Header().Add("Vary", "Accept-Language, Cookie")
		ctx := i18n.WithLocale(r.Context(), l)
		// The path, so the language switch can send the visitor back to the
		// page they were reading. RawQuery is deliberately dropped: a switch
		// that carried the query string back would also carry a search term
		// or a variant selection into a redirect target, and the smaller the
		// value the less there is to validate.
		ctx = web.WithRequestPath(ctx, r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bannerFreePrefixes are the paths that never show a promotion.
//
// Checkout has one job and an offer beside it is a conversion risk — the
// discount field is already in the order summary. The account pages and the
// back office are task-oriented: somebody there came to do something, not to
// buy. Everything else is the storefront.
// /healthz and /readyz join them, and for a different reason from the rest: a
// probe is not a page and has no visitor to show a promotion to. Reading a
// banner for one is a database round trip on the endpoint an orchestrator hits
// every few seconds to decide whether this process is alive.
var bannerFreePrefixes = []string{
	"/checkout", "/cart", "/orders", "/account", "/admin",
	"/signin", "/register", "/forgot", "/reset", "/webhooks", "/media", "/static",
	"/healthz", "/readyz",
}

// withBanner attaches the promotional strip to storefront requests.
//
// One read per storefront page view. Not cached in memory, because a shop that
// switches a promotion off expects it gone — and the read is an indexed
// single-row lookup against a table with a handful of rows.
func withBanner(next http.Handler, store *home.Store, log *slog.Logger, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !storefrontPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		banner, err := store.Banner(r.Context(), home.ReadDismissal(r, secure))
		if err != nil {
			// Not fatal. Losing the strip is far smaller than losing the page,
			// and an absent banner renders as nothing at all.
			log.ErrorContext(r.Context(), "read promo banner", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(layouts.WithBanner(r.Context(), banner)))
	})
}

// withTopNav loads the header's category row.
//
// Inside withLocale, because the names it reads are localized and the locale has
// to be on the context before the query runs. Skipped for /admin, which has its
// own shell and no category nav, and for anything that is not a GET — a POST
// answers 303 and renders no chrome.
//
// A failure is not fatal, exactly like the banner's: an empty nav renders as a
// header with no category links, which is far smaller than losing the page.
func withTopNav(next http.Handler, store *home.Store, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The same exclusions the banner uses, and it needed them more.
		//
		// This skipped only non-GET and /admin, so `GET /healthz`, `GET /readyz`
		// and every static asset and image request ran a category query — a
		// database round trip to build a navigation bar for a response that has
		// no navigation bar. Liveness in particular: the probe an orchestrator
		// uses to decide whether to restart this process was reaching the
		// database, which is the one dependency a liveness check must not have an
		// opinion about.
		//
		// It degrades rather than fails on error, so this was latency and not an
		// outage — but a slow database made the probe slow, and a slow probe is
		// how a healthy process gets killed.
		if r.Method != http.MethodGet || !storefrontPath(r.URL.Path) {
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
//
// A *payment.Gateway is never nil — a deployment without a Stripe key gets one
// that reports Enabled() false — so passing it straight through would make every
// cancellation on a keyless deployment log a Warn about ErrDisabled. There is no
// session to close there, because there was never a key to open one with.
//
// Returned as an explicitly nil INTERFACE rather than a nil *Gateway: a typed nil
// in an interface is non-nil, so the `if h.sessions == nil` in both handlers would
// not fire and the calls would go through to a disabled gateway anyway. That trap
// is the whole reason this is a function instead of a conditional argument.
//
// The return type is cart's, and admin's identical interface takes it by ordinary
// assignment. Two consumers naming the same one method is the pattern, not an
// oversight: neither package should import the other for a signature.
func sessionCloser(g *payment.Gateway) cart.SessionCloser {
	if g == nil || !g.Enabled() {
		return nil
	}
	return g
}
