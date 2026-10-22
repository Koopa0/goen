package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping/storemap"
)

// routeConstants resolves the named constants a route pattern is built from.
// They are the real constants, so a renamed value cannot leave this map stale;
// a constant added to a pattern must be added here, and storefrontPatterns
// says so when it meets one it cannot resolve.
var routeConstants = map[string]string{
	"assets.Prefix":              assets.Prefix,
	"storemap.ReturnPath":        storemap.ReturnPath,
	"checkout.PickupStartAction": checkout.PickupStartAction,
	"checkout.PickupMapPath":     checkout.PickupMapPath,
}

// routeInventoryFloor is the fewest non-test files the route walk must have
// parsed. On the tree this was written against the walk reads 607 files, so a
// walk that quietly reads none (a wrong root, a skip rule that swallowed a
// directory) fails instead of reporting an empty route table.
const routeInventoryFloor = 540

// storefrontPatterns returns, sorted, every route pattern the production code
// registers outside the back office, read from the source because
// [http.ServeMux] cannot list its patterns. backOfficePatterns returns the
// ones whose path begins with /admin; both come from one walk.
//
// It parses, without resolving objects, every non-test .go file under the
// repository's cmd/goen and internal directories, skipping directories whose
// name starts with "." or "_", and testdata. Test files are out of scope on
// purpose: a test that registers a route on a mux of its own is not production
// routing, and counting it would put a route in the inventory that no visitor
// can reach. A registration the walk finds only in a test file is therefore not
// reported, and a guard built on this list says nothing about test helpers.
//
// A registration is a call of HandleFunc or Handle whose first argument is a
// string literal "<METHOD> /…", or such a literal plus a constant that
// routeConstants resolves ("GET "+assets.Prefix, or "POST "+PickupStartAction
// inside checkout). Any other first argument is
// not a route, and the call is skipped: the outbox's Store.Handle names a topic.
func storefrontPatterns(t *testing.T) []string {
	t.Helper()
	storefront, _ := routePatterns(t)
	return storefront
}

// backOfficePatterns is the /admin half of the walk storefrontPatterns describes.
func backOfficePatterns(t *testing.T) []string {
	t.Helper()
	_, backOffice := routePatterns(t)
	return backOffice
}

func routePatterns(t *testing.T) (storefront, backOffice []string) {
	t.Helper()

	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	parsed := 0

	for _, top := range []string{"cmd/goen", "internal"} {
		dir := filepath.Join(root, filepath.FromSlash(top))
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			name := d.Name()
			if d.IsDir() {
				if name == "testdata" ||
					(rel != top && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"))) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			parsed++
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				if pattern, ok := routePattern(t, fset, file.Name.Name, call); ok {
					if strings.HasPrefix(patternPath(pattern), "/admin") {
						backOffice = append(backOffice, pattern)
					} else {
						storefront = append(storefront, pattern)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}

	if parsed < routeInventoryFloor {
		t.Fatalf("the walk parsed %d non-test files, want at least %d: it is reading the wrong tree", parsed, routeInventoryFloor)
	}
	slices.Sort(storefront)
	slices.Sort(backOffice)
	return slices.Compact(storefront), slices.Compact(backOffice)
}

// routePattern returns the pattern a HandleFunc or Handle call in package pkg
// registers, or false when the call is not a route registration. A constant
// named without a qualifier is pkg's own, so checkout's "POST "+PickupStartAction
// resolves as checkout.PickupStartAction does in any other package.
func routePattern(t *testing.T, fset *token.FileSet, pkg string, call *ast.CallExpr) (string, bool) {
	t.Helper()

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
		return "", false
	}
	switch arg := call.Args[0].(type) {
	case *ast.BasicLit:
		pattern, ok := methodLiteral(arg)
		return pattern, ok && patternPath(pattern) != ""
	case *ast.BinaryExpr:
		if arg.Op != token.ADD {
			return "", false
		}
		lit, ok := arg.X.(*ast.BasicLit)
		if !ok {
			return "", false
		}
		method, ok := methodLiteral(lit)
		if !ok {
			return "", false
		}
		var key string
		switch ref := arg.Y.(type) {
		case *ast.SelectorExpr:
			qualifier, ok := ref.X.(*ast.Ident)
			if !ok {
				return "", false
			}
			key = qualifier.Name + "." + ref.Sel.Name
		case *ast.Ident:
			key = pkg + "." + ref.Name
		default:
			return "", false
		}
		value, known := routeConstants[key]
		if !known {
			t.Fatalf("unknown constant %s in a route pattern at %s: add it to the resolver", key, fset.Position(arg.Pos()))
		}
		return method + value, true
	default:
		return "", false
	}
}

// methodLiteral reads a string literal of the form "<METHOD> /…". A literal
// that stops at the method ("GET ") is returned as it is, for the caller to
// join with a constant.
func methodLiteral(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	method, path, ok := strings.Cut(s, " ")
	if !ok || !isHTTPMethod(method) {
		return "", false
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		return "", false
	}
	return s, true
}

func isHTTPMethod(s string) bool {
	switch s {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}

// patternPath is the path half of a "<METHOD> /path" pattern.
func patternPath(pattern string) string {
	_, path, _ := strings.Cut(pattern, " ")
	return path
}

func TestStorefrontPatternsAreReadFromTheSource(t *testing.T) {
	t.Parallel()

	patterns := storefrontPatterns(t)
	if len(patterns) < 90 {
		t.Errorf("found %d storefront patterns, want at least 90", len(patterns))
	}
	for _, want := range []string{
		"GET /cart",
		"POST " + storemap.ReturnPath,
		"GET /static/",
	} {
		if !slices.Contains(patterns, want) {
			t.Errorf("%q is not among the patterns the walk found", want)
		}
	}
	for _, pattern := range patterns {
		if strings.HasPrefix(patternPath(pattern), "/admin") {
			t.Errorf("%q is a back-office route: TestEveryBackOfficeRouteRefusesAnOutsider owns those", pattern)
		}
	}
}

// The concrete values a row's sample puts in the path variables. None of them
// names a row in the database, so the probes read nothing and write nothing.
const (
	probeSlug    = "inventory-probe"
	probeNumber  = "GO-260101-000001"
	probeDigest  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	probeAddress = "7c1f2b9e-5d34-4a6e-9b0c-3e8a1d2f4c60"
)

// inventoryRow is one registered storefront pattern and what an anonymous
// request to it gets.
type inventoryRow struct {
	pattern string
	// sample is a concrete path the pattern serves.
	sample string
	// traits is a sorted, space-separated set from knownTraits.
	traits string
	// limiter names the budget a burst from one address spends on this route,
	// for a row whose traits include limited. Rows that spend one budget
	// between them carry one name, so a route that leaves a shared limiter, or
	// joins another, changes the grouping. The name is the table's own; the
	// grouping is what is observed.
	limiter string
	// burst is how many requests one fresh address is admitted before its
	// first 429, for a row whose traits include limited.
	burst int
}

// storefrontInventory has one row per storefront pattern. It is a record of
// what the router does today, not a judgement of it. A row marked "suspect" is
// observed behaviour that looks unintended; the mark makes changing it a
// conscious act, and nothing here says it is a bug. A change that adds,
// removes or re-wraps a storefront route, or edits a prefix list in
// middleware.go, edits the rows it moves in the same pull request.
var storefrontInventory = []inventoryRow{
	{pattern: "GET /", sample: "/inventory-no-such-page", traits: "banner nav speculation visitor"},
	{pattern: "GET /about", sample: "/about", traits: "banner nav speculation visitor"},
	{pattern: "GET /account", sample: "/account", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/addresses/{address}/edit", sample: "/account/addresses/" + probeAddress + "/edit", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/cart-recovery", sample: "/account/cart-recovery", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/orders/{number}", sample: "/account/orders/" + probeNumber, traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/points", sample: "/account/points", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/warranty", sample: "/account/warranty", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/warranty/{number}", sample: "/account/warranty/" + probeNumber, traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /account/wishlist", sample: "/account/wishlist", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "GET /auth/google", sample: "/auth/google", traits: "banner nav nostore visitor"},                   // Google is not configured in this test, so this answers the full 404 page, header included; configured, it answers 303
	{pattern: "GET /auth/google/callback", sample: "/auth/google/callback", traits: "banner nav nostore visitor"}, // Google is not configured in this test, so this answers the full 404 page, header included; configured, it answers 303
	{pattern: "GET /c/{slug}", sample: "/c/" + probeSlug, traits: "banner nav speculation visitor"},
	{pattern: "GET /cart", sample: "/cart", traits: "nav nostore speculation visitor"},
	{pattern: "GET /checkout", sample: "/checkout", traits: "nav nostore visitor"},
	{pattern: "GET /checkout/pickup/map", sample: "/checkout/pickup/map", traits: "nav nostore visitor"},
	{pattern: "GET /compare", sample: "/compare", traits: "banner nav speculation visitor"},
	{pattern: "GET /contact", sample: "/contact", traits: "banner nav speculation visitor"},
	{pattern: "GET /deals", sample: "/deals", traits: "banner nav speculation visitor"},
	{pattern: "GET /faq", sample: "/faq", traits: "banner nav speculation visitor"},
	{pattern: "GET /favicon.ico", sample: "/favicon.ico", traits: ""},
	{pattern: "GET /forgot", sample: "/forgot", traits: "nav nostore visitor"},
	{pattern: "GET /healthz", sample: "/healthz", traits: "nostore"},
	{pattern: "GET /media/{digest}", sample: "/media/" + probeDigest, traits: ""},
	{pattern: "GET /media/{digest}/{width}", sample: "/media/" + probeDigest + "/400", traits: ""},
	{pattern: "GET /newsletter/confirm", sample: "/newsletter/confirm", traits: "banner nav nostore visitor"},
	{pattern: "GET /newsletter/thanks", sample: "/newsletter/thanks", traits: "banner nav nostore visitor"},
	{pattern: "GET /newsletter/unsubscribe", sample: "/newsletter/unsubscribe", traits: "banner nav nostore visitor"},
	{pattern: "GET /orders/find", sample: "/orders/find", traits: "nav nostore visitor"},
	{pattern: "GET /orders/{number}", sample: "/orders/" + probeNumber, traits: "nav nostore visitor"},
	{pattern: "GET /orders/{number}/pay", sample: "/orders/" + probeNumber + "/pay", traits: "nav nostore visitor"},
	{pattern: "GET /orders/{number}/return", sample: "/orders/" + probeNumber + "/return", traits: "nav nostore visitor"},
	{pattern: "GET /p/{slug}", sample: "/p/" + probeSlug, traits: "banner nav speculation visitor"},
	{pattern: "GET /payment", sample: "/payment", traits: "banner nav speculation visitor"},
	{pattern: "GET /privacy", sample: "/privacy", traits: "banner nav speculation visitor"},
	{pattern: "GET /readyz", sample: "/readyz", traits: "nostore"},
	{pattern: "GET /register", sample: "/register", traits: "nav nostore visitor"},
	{pattern: "GET /register/complete", sample: "/register/complete", traits: "nav nostore visitor"},
	{pattern: "GET /reset", sample: "/reset", traits: "nav nostore visitor"},
	{pattern: "GET /returns", sample: "/returns", traits: "banner nav speculation visitor"},
	{pattern: "GET /robots.txt", sample: "/robots.txt", traits: "visitor"}, // still visitor: the sitemap needs the request budget, which stateless paths skip, and robots.txt follows it
	{pattern: "GET /s/{slug}", sample: "/s/" + probeSlug, traits: "banner nav speculation visitor"},
	{pattern: "GET /search", sample: "/search", traits: "banner nav speculation visitor"},
	{pattern: "GET /shipping", sample: "/shipping", traits: "banner nav speculation visitor"},
	{pattern: "GET /signin", sample: "/signin", traits: "nav nostore visitor"},
	{pattern: "GET /sitemap.xml", sample: "/sitemap.xml", traits: "visitor"}, // still visitor: it needs the request budget, which stateless paths skip
	{pattern: "GET /static/", sample: "/static/css/app/app.css", traits: ""},
	{pattern: "GET /terms", sample: "/terms", traits: "banner nav speculation visitor"},
	{pattern: "GET /verify", sample: "/verify", traits: "banner nav nostore visitor"}, // shows the banner like the other mailed-link pages that share its template (/newsletter/confirm, /newsletter/unsubscribe); the account-recovery forms /forgot and /reset are banner-free
	{pattern: "GET /warranty", sample: "/warranty", traits: "banner nav speculation visitor"},
	{pattern: "GET /{$}", sample: "/", traits: "banner nav speculation visitor"},
	{pattern: "POST /account/addresses", sample: "/account/addresses", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/addresses/default", sample: "/account/addresses/default", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/addresses/delete", sample: "/account/addresses/delete", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/addresses/update", sample: "/account/addresses/update", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/cart/retry", sample: "/account/cart/retry", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/email", sample: "/account/email", traits: "limited logged nav nostore signin signinreturn visitor", limiter: "auth", burst: 20},
	{pattern: "POST /account/email/resend", sample: "/account/email/resend", traits: "limited logged nav nostore signin signinreturn visitor", limiter: "auth", burst: 20},
	{pattern: "POST /account/erase", sample: "/account/erase", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/google/unlink", sample: "/account/google/unlink", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/password", sample: "/account/password", traits: "limited logged nav nostore signin signinreturn visitor", limiter: "auth", burst: 20},
	{pattern: "POST /account/password/setup", sample: "/account/password/setup", traits: "limited logged nav nostore signin visitor", limiter: "auth", burst: 20},
	{pattern: "POST /account/points", sample: "/account/points", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/profile", sample: "/account/profile", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/sessions/others/end", sample: "/account/sessions/others/end", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/warranty/{number}", sample: "/account/warranty/" + probeNumber, traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /account/wishlist", sample: "/account/wishlist", traits: "nav nostore signin signinreturn visitor"},
	{pattern: "POST /cart/items", sample: "/cart/items", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /cart/items/update", sample: "/cart/items/update", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /checkout", sample: "/checkout", traits: "limited logged nav nostore visitor", limiter: "checkout", burst: 30},
	{pattern: "POST /checkout/pickup/return", sample: "/checkout/pickup/return", traits: "nostore visitor"},
	{pattern: "POST /checkout/pickup/start", sample: "/checkout/pickup/start", traits: "limited logged nav nostore visitor", limiter: "checkout", burst: 30},
	{pattern: "POST /contact", sample: "/contact", traits: "limited nav nostore visitor", limiter: "contact", burst: 5},
	{pattern: "POST /forgot", sample: "/forgot", traits: "limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /locale", sample: "/locale", traits: "nav nostore visitor"},
	{pattern: "POST /newsletter", sample: "/newsletter", traits: "limited logged nav nostore visitor", limiter: "signup", burst: 2},
	{pattern: "POST /newsletter/confirm", sample: "/newsletter/confirm", traits: "nav nostore visitor"},
	{pattern: "POST /newsletter/unsubscribe", sample: "/newsletter/unsubscribe", traits: "nav nostore visitor"},
	{pattern: "POST /orders/find", sample: "/orders/find", traits: "limited logged nav nostore visitor", limiter: "find", burst: 10},
	{pattern: "POST /orders/{number}/cancel", sample: "/orders/" + probeNumber + "/cancel", traits: "nav nostore visitor"},
	{pattern: "POST /orders/{number}/pay", sample: "/orders/" + probeNumber + "/pay", traits: "nav nostore visitor"},
	{pattern: "POST /orders/{number}/reorder", sample: "/orders/" + probeNumber + "/reorder", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /orders/{number}/return", sample: "/orders/" + probeNumber + "/return", traits: "nav nostore visitor"},
	{pattern: "POST /p/{slug}/notify", sample: "/p/" + probeSlug + "/notify", traits: "limited logged nav nostore visitor", limiter: "notify", burst: 5},
	{pattern: "POST /p/{slug}/questions", sample: "/p/" + probeSlug + "/questions", traits: "nav nostore signin visitor"},
	{pattern: "POST /p/{slug}/reviews", sample: "/p/" + probeSlug + "/reviews", traits: "nav nostore signin visitor"},
	{pattern: "POST /promo/dismiss", sample: "/promo/dismiss", traits: "nav nostore visitor"},
	{pattern: "POST /register", sample: "/register", traits: "clearsite limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /register/complete", sample: "/register/complete", traits: "clearsite limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /register/resend", sample: "/register/resend", traits: "limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /reset", sample: "/reset", traits: "limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /signin", sample: "/signin", traits: "clearsite limited nav nostore visitor", limiter: "auth", burst: 6},
	{pattern: "POST /signout", sample: "/signout", traits: "clearcache clearsite nav nostore visitor"},
	{pattern: "POST /verify", sample: "/verify", traits: "limited logged nav nostore visitor", limiter: "auth", burst: 20},
	{pattern: "POST /webhooks/stripe", sample: "/webhooks/stripe", traits: "nostore"},
}

// inventoryRequest is the anonymous request a row describes: no cookies, and
// for a write a form-encoded empty body.
func inventoryRequest(t *testing.T, row inventoryRow, id string) *http.Request {
	t.Helper()

	method, _, _ := strings.Cut(row.pattern, " ")
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequestWithContext(t.Context(), method, row.sample, http.NoBody)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, row.sample, strings.NewReader(""))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Request-Id", id)
	return req
}

// checkInventoryCoversTheRoutes holds the two halves of the inventory that need
// no database: the table lists exactly the patterns the source registers, and
// on mux, production's own, each row's sample reaches its own pattern rather
// than the catch-all. The second half is what notices a pattern the source
// still spells but production no longer registers: a feature's Routes that
// nobody calls, or a registration behind a branch that does not run.
func checkInventoryCoversTheRoutes(t *testing.T, mux *http.ServeMux) {
	t.Helper()

	registered := storefrontPatterns(t)
	tabled := make([]string, 0, len(storefrontInventory))
	for _, row := range storefrontInventory {
		tabled = append(tabled, row.pattern)
	}
	slices.Sort(tabled)
	for i := 1; i < len(tabled); i++ {
		if tabled[i] == tabled[i-1] {
			t.Errorf("%s has two rows in the inventory", tabled[i])
		}
	}
	for _, p := range registered {
		if !slices.Contains(tabled, p) {
			t.Errorf("new storefront route %s is not in the inventory: add its row with the traits it gets", p)
		}
	}
	for _, p := range tabled {
		if !slices.Contains(registered, p) {
			t.Errorf("%s is in the inventory but no longer registered", p)
		}
	}

	// Each sample reaches its own pattern, and not the catch-all.
	for _, row := range storefrontInventory {
		if _, got := mux.Handler(inventoryRequest(t, row, "inv-sample")); got != row.pattern {
			t.Errorf("%s: the sample %s reaches %q instead", row.pattern, row.sample, got)
		}
	}
}

// idleInventoryMux is production's mux on a pool that never connects, with the
// store map configured so its routes are registered. Building the router
// reaches no database; only serving does.
func idleInventoryMux(t *testing.T) *http.ServeMux {
	t.Helper()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}
	storeMap, err := storemap.New(testMerchantID, string(storemap.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	_, mux := routing(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: idle, Payments: gateway, BaseURL: "https://goen.test", StoreMap: storeMap},
		BackOffice: BackOfficeConfig{AdminPool: idle, Payments: gateway, Refunder: gateway, StoreMap: storeMap},
	}, slog.New(slog.DiscardHandler))
	return mux
}

// TestEveryStorefrontRouteHasAnInventoryRow fails the moment a storefront route
// is added, removed or renamed without its row: the walk's pattern count is
// compared with the table's, name by name, with no database needed.
func TestEveryStorefrontRouteHasAnInventoryRow(t *testing.T) {
	t.Parallel()

	checkInventoryCoversTheRoutes(t, idleInventoryMux(t))
	if walked := len(storefrontPatterns(t)); walked != len(storefrontInventory) {
		t.Errorf("the walk found %d storefront patterns, the inventory has %d rows", walked, len(storefrontInventory))
	}
}

// backOfficeRouteFloor is the fewest /admin patterns the walk must find; it
// finds 114 on the tree this was written against. A walk that reads none of the
// desks would otherwise ask the mux about no route and pass.
const backOfficeRouteFloor = 100

// TestEveryBackOfficeRouteRefusesAnOutsider asks production's own mux each
// /admin route the source registers, signed out and as a customer, and wants
// the guard's 404 page from both. A desk's own routes_test.go checks only the
// routes it is given, so a route added to a desk without the staff wrapper, or
// without a line in that list, is caught here.
func TestEveryBackOfficeRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()

	routes := backOfficePatterns(t)
	if len(routes) < backOfficeRouteFloor {
		t.Fatalf("found %d back-office patterns, want at least %d: the walk reads too little", len(routes), backOfficeRouteFloor)
	}
	accesstest.RefuseOutsidersOn(t, idleInventoryMux(t), routes...)
}
