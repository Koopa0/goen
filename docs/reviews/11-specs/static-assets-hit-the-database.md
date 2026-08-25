# static-assets-hit-the-database

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** cmd/goen/server.go, cmd/goen/server_test.go, cmd/goen/integration_test.go


## Root cause

The middleware chain is composed over the whole mux, so its membership is decided by position in `newRouter` rather than by what a route needs. Two of the four chrome middlewares already correct for this per-request (`withTopNav`/`withBanner` consult `navFreePrefixes`/`bannerFreePrefixes`, which name `/static` and `/media`); the two that read per-visitor state from the database do not. The knowledge "these paths belong to no visitor" exists twice in the file and was never applied to the layers where it costs a query.


## Reproduction — the evidence this rests on

EXECUTED against the running dev server and the live database; the cited lines were also re-read.

Lines confirmed (all quotes verbatim):
- cmd/goen/server.go:122-123 — `mux := http.NewServeMux()` / `mux.Handle("GET "+assets.Prefix, assets.Handler())`, and `assets.Prefix = "/static/"` (assets/assets.go:29).
- cmd/goen/server.go:129-130 — `mux.HandleFunc("GET /media/{digest}", images.Serve)` and `GET /media/{digest}/{width}`.
- cmd/goen/server.go:332-343 — the chain wraps the WHOLE mux:
    var handler http.Handler = mux
    handler = withBanner(...); handler = withTopNav(...); handler = withLocale(...)
    handler = basket.WithCount(handler)
    handler = customers.Authenticate(handler)
    handler = crossOriginProtection(handler); handler = securityHeaders(handler)
    handler = requestLog(handler, log); handler = withRequestID(handler)
    return recoverPanic(handler, log)
  Comment above it: "Applied inner to outer, so a request passes through them in the reverse of this order." So request order is recoverPanic → withRequestID → requestLog → securityHeaders → crossOriginProtection → Authenticate → WithCount → withLocale → withTopNav → withBanner → mux. Both DB middlewares run before routing.
- internal/account/handler.go:73-83 — `Authenticate` returns early only when `ReadSessionCookie(r, h.secure) == ""`; otherwise `h.store.SessionUser(r.Context(), token)`.
- internal/cart/handler.go:748-758 + 763-778 — `WithCount` → `CartIDForRequest` → `h.store.Find(ctx, token)` (any non-empty cookie), then `h.store.ItemCount` only when Find resolved.
- cmd/goen/server.go:492-495 `bannerFreePrefixes` and 524-526 `navFreePrefixes` both list `"/media", "/static"` (plus `/healthz`, `/readyz`, `/webhooks`). The two expensive middlewares have no such list. The summary's approximate line numbers (~494, ~525) are right.

Measurement (delta of `seq_scan+idx_scan` from pg_stat_user_tables, 8s settle between samples — the stats collector lags, which produced two misleading runs before I settled it):
  base:   carts=3936 sessions=1889
  10x GET /static/css/app/app.css with `Cookie: goen_cart=x; goen_session=y`  → carts=3946 sessions=1899   (+1 carts, +1 sessions per request)
  10x GET /media/abc123 with the same cookies                                → carts=3956 sessions=1909   (+1, +1)
  10x GET /static/css/app/app.css with NO cookies                            → carts=3956 sessions=1909   (0)
So: 2 round trips per asset request for a visitor holding a cart cookie and a session cookie; a resolving cart cookie adds ItemCount for a third (`SessionUser` joins users, which is why the reviewer counted 3 table touches). Zero with no cookies — the cookie-empty early return is the only thing saving an anonymous visitor.

Page cost confirmed exactly: `curl -s http://127.0.0.1:9700/ | grep -oE '(src|href)="/(static|media)[^"]*"' | sort -u | wc -l` = 16. A signed-in visitor with a cart therefore pays 32–48 discarded queries per home-page view, none of whose results any of those handlers reads (`internal/media` and `assets` reference neither `account.FromContext` nor `web.CartCount`; grep shows internal/media imports no i18n either).

One thing the summary understates: the same chain also puts `Vary: Accept-Language, Cookie` on every asset response (from `withLocale`, server.go:485), while `assets.Handler` sets `Cache-Control: public, max-age=31536000, immutable` for a matching `?v=` digest (assets/assets.go:218). Verified on the wire:
  Cache-Control: no-cache            (wrong ?v=)
  Vary: Accept-Language, Cookie
A `Vary: Cookie` on an immutable, digest-versioned asset makes a shared cache key on every distinct session cookie, i.e. a 0% hit rate for exactly the assets designed to be cached for a year. That is the same structural defect, one middleware further out, and belongs in the same fix.

Nothing in CLAUDE.md or docs/roadmap.md records this as a decision; the only nearby recorded decisions are the two prefix lists, whose comments say the opposite ("The probes are here so no orchestrator health check makes a database round trip" — server.go:490-491).

Working tree verified clean: `git status --porcelain` empty.


## Blast radius

Every static subresource of every page, for every visitor holding a cart or session cookie — which after one "add to cart" is every returning visitor. 16 same-origin subresources on the home page × 2–3 discarded queries each = 32–48 pool checkouts per page view that no handler reads. The pool is `pgx` default MaxConns (cmd/goen/main.go:373-375, `openPoolAs(ctx, url, "store", 0)` and the comment "maxConns of 0 keeps pgx's default"), i.e. max(4, NumCPU) — on a 4-core box, 4 connections. A browser opens 6 parallel connections per origin, so one signed-in visitor loading one cold page can saturate the storefront pool with work whose results are thrown away, and the page's own real queries queue behind it. Silent: it costs latency and connections, never an error, and no gate here can see it — every guard in this repository asks whether something is absent. Second, invisible half: `Vary: Cookie` on year-immutable assets defeats any shared cache in front of goen, so the digest-versioning scheme in assets/assets.go buys nothing for a CDN.


## Fix

DECISION: take the prefix-filter approach (option B), implemented as a wrapper in `cmd/goen/server.go`. Do NOT mount /static and /media on a mux outside the chain.

Why B over A — state this in the code comment:
- /media serves bytes that arrived from an upload form. `securityHeaders` (server.go:395-403) is what puts `X-Content-Type-Options: nosniff` and the CSP on that response; `requestLog`, `withRequestID` and `recoverPanic` must also stay. Mounting outside the chain means either dropping those or building a second chain — a second place for nosniff and the CSP to be forgotten on the one route that serves attacker-supplied content. B keeps every header-and-observability layer and removes exactly the two that make a query.
- The route table stays in one mux, so `internal/ui/pages/links_test.go:60` (`regexp.MustCompile("mux\\.HandleFunc\\(\"GET (/[^\"]*)\"")`) keeps reading the same corpus. A would silently shrink it (it would still clear the `len(routes) < 20` floor, so nothing would go red).
- B is the shape already in the file, one list beside two others, and the containment guard below binds the three.
Cost of B, name it honestly: a fourth prefix list somebody must extend. Its failure mode is a performance regression, not a correctness one, and the guard below catches the inconsistent half.

1. Add the list, beside the other two (immediately after `navFreePrefixes`, cmd/goen/server.go:526):

// statelessPrefixes are the paths that belong to no visitor: goen's own bytes,
// the probes, and the provider callback. Nothing under them reads the signed-in
// user or the cart badge, so the two middlewares that go to the DATABASE for
// those are skipped rather than run and discarded. The home page pulls sixteen
// subresources; without this a returning visitor pays two round trips on each.
//
// Every entry here is also in navFreePrefixes and bannerFreePrefixes, and
// TestNothingStatelessRendersChrome holds that: a path with no visitor state
// cannot need a header or a promotion. /admin is deliberately NOT here — it
// renders no storefront chrome but RequireStaff reads the session Authenticate
// puts on the context.
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

Note `assets.Prefix` is "/static/"; matching on the trimmed "/static" plus the `+"/"` rule is what `navPath`/`storefrontPath` already do, so reuse the shape rather than inventing a second matcher.

2. Add the wrapper, beside `statelessPath`:

// onlyVisitorPaths applies mw to the requests that have a visitor, and runs
// next directly for the rest. The filter is here rather than inside
// internal/cart and internal/account because which URLs carry a visitor is a
// ROUTING fact, and those packages have no business knowing goen's paths.
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

3. Rewrite three lines of the chain (cmd/goen/server.go:337-339). From:
	handler = withLocale(handler, secureCookies)
	handler = basket.WithCount(handler)
	handler = customers.Authenticate(handler)
to:
	handler = onlyVisitorPaths(func(next http.Handler) http.Handler {
		return withLocale(next, secureCookies)
	}, handler)
	handler = onlyVisitorPaths(basket.WithCount, handler)
	handler = onlyVisitorPaths(customers.Authenticate, handler)

`basket.WithCount` and `customers.Authenticate` are method values of type `func(http.Handler) http.Handler` and pass directly. Order is unchanged; each layer decides independently, so a stateless path bypasses all three.

withLocale is included for a reason of its own, and it must be in the comment: it adds `Vary: Accept-Language, Cookie` (server.go:485), and `assets.Handler` sets `Cache-Control: public, max-age=31536000, immutable` for a matching `?v=` digest (assets/assets.go:218). `Vary: Cookie` on an immutable asset keys a shared cache on every session cookie and reduces the hit rate to zero — the digest-versioning scheme buys nothing in front of a CDN. It is safe on these five prefixes and only these: `internal/media` imports no i18n (`grep i18n internal/media/*.go` is empty), `assets.Handler` renders no text, the probes answer plain text, and `Handler.Webhook` (internal/payment/handler.go:191-206) answers Stripe with `http.Error` literals, not `i18n.T`. `i18n.FromContext` falls back to `Default` (internal/i18n/i18n.go:68-74), so nothing panics if a future handler under one of those prefixes reads it — it would render Chinese, which is why the list must not grow to any path that renders a page.

DO NOT change internal/cart or internal/account. `WithCount` and `Authenticate` keep their current contract; nothing test-only is introduced (rules/interfaces).

One visible behaviour change, and it is already the precedent: a request that falls through to the mux's `GET /` NotFound page under one of these prefixes (e.g. `GET /media/` with a trailing slash) renders the 404 with no cart badge and no signed-in name. `navFreePrefixes` already strips the category row from exactly those paths, so the degradation is consistent with what ships today; `assets.Handler` and `media.Serve` answer their own misses with plain `http.NotFound`, so the case is nearly unreachable.

NOT IN THIS FIX — a separate finding, and say so in the PR rather than absorbing it:
- Pool sizing. `openPoolAs(ctx, url, "store", 0)` / `openAdminPool` (cmd/goen/main.go:373-382) keep pgx's default MaxConns, max(4, NumCPU), against a PostgreSQL whose own max_connections is unrelated to it. That is a capacity decision needing a measured number for the store, admin and maintenance pools plus what `/readyz` should say when the pool is exhausted. Bundling it would let a config change ride in on a two-line routing change and make the mutation proof below ambiguous.
- `statement_timeout`. Nothing sets one (`grep statement_timeout` over cmd/, internal/ and migrations/ is empty). The place for it is `cfg.ConnConfig.RuntimeParams` in `openPoolAs`, per-pool, and it needs its own argument about the `/admin/reports` 90-day window and the 584 ms `refresh_copurchases` on the maintenance pool — a timeout picked for the storefront would kill the projection rebuild. Its own finding, its own measurement.
Removing this load makes both LESS urgent, which is another reason not to merge them: fixing the leak first changes the number the sizing work would be based on.


## The lock, and how to see it fail first

Two unit tests in cmd/goen/server_test.go (package main, no Docker — this file already tests the chain in isolation and runs under `make verify`), plus one integration lock in cmd/goen/integration_test.go. Every one is proven by mutation per .claude/rules/testing.md.

1. TestAnAssetRequestNeverReachesThePerVisitorMiddleware — cmd/goen/server_test.go

Table-driven over `onlyVisitorPaths` with a counting stand-in, so it tests the composition rather than the cart and account packages:

	var ran int
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ran++
			next.ServeHTTP(w, r)
		})
	}
	var served int
	h := onlyVisitorPaths(counting, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served++ }))

Cases, each asserting BOTH counters (served must be 1 in every case — a bypass that swallows the request is the other way to pass):
  skipped (wantRan 0): /static/css/app/app.css, /static/css/app/app.css?v=abc, /static/js/goen.js, /media/0123abc, /media/0123abc/400, /healthz, /readyz, /webhooks/stripe
  applied (wantRan 1): /, /c/phones, /p/aurora-slate, /cart, /checkout, /account, /account/orders/G2601-0001, /signin, /orders/find, /admin, /admin/orders, /staticky (a path that merely starts with the letters — the prefix must be segment-bounded), /mediation

The /admin and /cart rows are the load-bearing half: they are what goes red if somebody widens the list to a path that needs the session, which is the way this fix breaks authorisation rather than performance.

Mutations, each must be SEEN red:
  a) In `onlyVisitorPaths`, delete the `if statelessPath(...)` block so mw always runs → every skipped row fails with ran=1.
  b) Invert it (`if !statelessPath`) → every applied row fails, /admin among them.
  c) Drop `"/media"` from `statelessPrefixes` → the two /media rows fail and nothing else does.
  d) In `statelessPath`, replace `path == prefix || strings.HasPrefix(path, prefix+"/")` with a bare `strings.HasPrefix(path, prefix)` → /staticky and /mediation fail.

2. TestNothingStatelessRendersChrome — cmd/goen/server_test.go

Asserts containment: every entry of `statelessPrefixes` appears in both `navFreePrefixes` and `bannerFreePrefixes`. That is the rule underneath the three lists — a path with no visitor state cannot need a header or a promotion — and it is what stops them drifting apart the way a fourth hand-maintained list otherwise would.
  Mutation: append "/cart" to `statelessPrefixes` → red on the nav half (/cart is in bannerFreePrefixes and not in navFreePrefixes), which is precisely the dangerous edit.
  Note the converse is deliberately NOT asserted: navFreePrefixes contains /admin, which must stay out of statelessPrefixes. Say so in the test comment or the next reader will "fix" it into an equality.

3. TestAnAssetRequestAsksTheDatabaseNothing — cmd/goen/integration_test.go (//go:build integration)

The end-to-end lock, and the only one that would have caught the original defect, because 1 and 2 both trust that the chain is wired with the wrapper. Count at the driver, not in pg_stat_user_tables — the stats collector lags by seconds and made two of my own runs read backwards.

  - Build a second pool from the same container DSN as TestMain, with a query tracer:
        cfg, _ := pgxpool.ParseConfig(dsn)   // capture dsn into a package var in TestMain; it is currently a local
        var queries atomic.Int64
        cfg.ConnConfig.Tracer = countingTracer{&queries}
        counted, _ := pgxpool.NewWithConfig(ctx, cfg)
    `countingTracer` implements pgx.QueryTracer (TraceQueryStart increments and returns ctx; TraceQueryEnd is a no-op). This is a pgx hook, not a goen interface introduced for a test — rules/interfaces is satisfied.
  - `h := newRouter(counted, counted, gw, admin.NewRefunder(""), &RouterConfig{BaseURL: "http://127.0.0.1", SecureCookies: false}, slog.New(slog.DiscardHandler))` where `gw, _ := payment.NewGateway("", "", "http://127.0.0.1")` — verified constructible: NewGateway with a blank key returns a disabled non-nil gateway (internal/payment/stripe.go:29-31), payment.NewHandler panics only on nil (handler.go:37), admin.NewStore needs a non-nil Refunder and NewRefunder("") supplies one (internal/admin/refund.go:39-45), and Invoices/Google nil are handled (`cfg.Invoices.Enabled()` is nil-safe, internal/invoice/ecpay.go:70).
  - Serve, through httptest.NewServer(h) or straight to ServeHTTP, with `Cookie: goen_cart=notarealtoken; goen_session=notarealtoken` (insecure cookie names, since SecureCookies is false):
        GET /static/css/app/app.css   → want 200 and queries delta 0
        GET /media/00112233445566778899aabbccddeeff → want 404 and queries delta 0
        GET /                          → want queries delta > 0
    The last row is what stops the test passing because the router was never wired to a database at all; without it a broken pool would read as success.
  - Assert the response of the /static row still carries `X-Content-Type-Options: nosniff` and a `Content-Security-Policy`, and does NOT carry `Vary`. That binds the two halves of the fix — the query skip and the cache-key skip — and refuses the "mount it outside the chain" refactor, which would drop the headers.

  Mutations, each seen red:
  e) Revert chain line 338 to `handler = basket.WithCount(handler)` → the two delta-0 rows fail with a non-zero count.
  f) Revert 337 to `handler = withLocale(handler, secureCookies)` → the Vary assertion fails while the query counts still pass, which is what proves the two halves are separately locked.
  g) Point the /static row at `/` → the delta-0 assertion fails, proving the instrument can see a query at all (the false-green this test's shape invites is a tracer that never fires).

Gates: `make verify` covers 1 and 2 (test-race, shuffled); `make test-integration` covers 3 and must be run — per CLAUDE.md, verify does not run it.
