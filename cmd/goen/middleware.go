package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/compress"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shipping/storemap"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

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
		compress.Skip(w)
		next.ServeHTTP(w, r)
	})
}

// withRequestBudget bounds pool acquisition and every database round trip on a
// request, storefront and back office alike, including the chrome middleware
// that shares the storefront pool. Stateless routes skip it: probes and
// webhooks must not spend a budget they never use.
func withRequestBudget(next http.Handler) http.Handler {
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
		protection.AddInsecureBypassPattern("POST " + storemap.ReturnPath)
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
func securityHeaders(next http.Handler, csp string, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
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

// unspeculated are the paths goen will not offer speculation rules from: every
// prefix in unstoredPrefixes except /cart. They are the signed-in account, the
// back office, an order and paying for it, the checkout, signing in,
// registering, recovering an account, and every page a mailed token opens.
//
// Each of those is read once, so nothing is gained by speculating from it, and
// a bug in the rules costs more there than anywhere else on the site. /cart is
// the deliberate exception, because shoppers browse from it back into the
// catalogue (#400).
//
// The rules document prerenders only /p/* and /c/*, so none of these is ever a
// target; this list governs the links on them, which is the second half of the
// same decision. TestEveryUnstoredPageButTheCartOffersNoSpeculation holds this
// list and unstoredPrefixes together.
//
// Prefixes, because every one of them owns its whole subtree.
var unspeculated = []string{
	"/checkout",
	"/orders/",
	"/account",
	"/admin",
	"/signin",
	"/register",
	"/forgot",
	"/reset",
	"/verify",
	"/auth/",
	"/newsletter/",
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
// header. Only a GET or HEAD of a page that renders the storefront header: a
// POST has already happened, and an asset, an uploaded image, a probe, the
// favicon, robots.txt, the sitemap and the back office render no storefront
// header and are not pages anybody browses from. The rules prerender the
// catalogue links such a page carries.
//
// The predicate is navPath on purpose, so a path added to navFreePrefixes loses
// the header too, with no fourth list to drift.
func speculates(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !navPath(r.URL.Path) {
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

// localeReturnPath is where a language switch sends the visitor back: the page
// they are on, query string included, so a search term, a filter or a chosen
// option survives the switch. The switch's handler still refuses anything but a
// same-site path.
func localeReturnPath(r *http.Request) string {
	if page, ok := catalog.ProductFormPage(r.URL.Path); ok {
		return page
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
		ctx = layouts.WithReturnPath(ctx, localeReturnPath(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bannerFreePrefixes are the paths that never show a promotion. The probes are
// here so no orchestrator health check makes a database round trip. robots.txt
// and the sitemap render no page, so no promotion is read for them.
var bannerFreePrefixes = []string{
	"/checkout", "/cart", "/orders", "/account", "/admin",
	"/signin", "/register", "/forgot", "/reset", "/webhooks", "/media", "/static",
	"/healthz", "/readyz", "/favicon.ico", "/robots.txt", "/sitemap.xml",
}

// withBanner runs banner, site's promotion strip, on storefront GETs only.
func withBanner(next http.Handler, banner func(http.Handler) http.Handler) http.Handler {
	shown := banner(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !storefrontPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		shown.ServeHTTP(w, r)
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
	"/robots.txt", "/sitemap.xml",
	storemap.ReturnPath,
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

// withTopNav runs nav, site's header category row, on the nav paths. It runs
// inside withLocale, because the names it reads are localized and the locale
// has to be on the context before the query does.
//
// Every method, not just GET: a rejected form re-renders its own page at 422,
// which is exactly when a visitor is most likely to navigate away.
func withTopNav(next http.Handler, nav func(http.Handler) http.Handler) http.Handler {
	shown := nav(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !navPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		shown.ServeHTTP(w, r)
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
		u, ok := user.FromContext(r.Context())
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
	if _, signedIn := user.FromContext(r.Context()); signedIn {
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
