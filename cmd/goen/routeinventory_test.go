package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/ui/pages"
)

// routeConstants resolves the named constants a route pattern is built from.
// They are the real constants, so a renamed value cannot leave this map stale;
// a constant added to a pattern must be added here, and storefrontPatterns
// says so when it meets one it cannot resolve.
var routeConstants = map[string]string{
	"assets.Prefix":           assets.Prefix,
	"cart.PickupReturnPath":   cart.PickupReturnPath,
	"pages.PickupStartAction": pages.PickupStartAction,
	"pages.PickupMapPath":     pages.PickupMapPath,
}

// routeInventoryFloor is the fewest non-test files storefrontPatterns must have
// parsed. On the tree this was written against the walk reads 409 files, so a
// walk that quietly reads none (a wrong root, a skip rule that swallowed a
// directory) fails instead of reporting an empty route table.
const routeInventoryFloor = 380

// storefrontPatterns returns, sorted, every route pattern the production code
// registers outside the back office, read from the source because
// [http.ServeMux] cannot list its patterns.
//
// It parses, without resolving objects, every non-test .go file under the
// repository's cmd/goen and internal directories. It skips internal/admin,
// which accesstest owns, directories whose name starts with "." or "_", and
// testdata. Test files are out of scope on purpose: a test that registers a
// route on a mux of its own is not production routing, and counting it would
// put a route in the inventory that no visitor can reach. A registration the
// walk finds only in a test file is therefore not reported, and a guard built
// on this list says nothing about test helpers.
//
// A registration is a call of HandleFunc or Handle whose first argument is a
// string literal "<METHOD> /…", or such a literal plus a selector that
// routeConstants resolves ("GET "+assets.Prefix). Any other first argument is
// not a route, and the call is skipped: the outbox's Store.Handle names a topic.
// A pattern whose path begins with /admin is a back-office route and is skipped.
func storefrontPatterns(t *testing.T) []string {
	t.Helper()

	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var patterns []string
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
				if rel == "internal/admin" || name == "testdata" ||
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
				if pattern, ok := routePattern(t, fset, call); ok && !strings.HasPrefix(patternPath(pattern), "/admin") {
					patterns = append(patterns, pattern)
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
	slices.Sort(patterns)
	return slices.Compact(patterns)
}

// routePattern returns the pattern a HandleFunc or Handle call registers, or
// false when the call is not a route registration.
func routePattern(t *testing.T, fset *token.FileSet, call *ast.CallExpr) (string, bool) {
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
		ref, ok := arg.Y.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		qualifier, ok := ref.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		key := qualifier.Name + "." + ref.Sel.Name
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
		"POST " + cart.PickupReturnPath,
		"GET /static/",
	} {
		if !slices.Contains(patterns, want) {
			t.Errorf("%q is not among the patterns the walk found", want)
		}
	}
	for _, pattern := range patterns {
		if strings.HasPrefix(patternPath(pattern), "/admin") {
			t.Errorf("%q is a back-office route: accesstest owns those", pattern)
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
	{pattern: "GET /account", sample: "/account", traits: "nav nostore signin visitor"},
	{pattern: "GET /account/addresses/{address}/edit", sample: "/account/addresses/" + probeAddress + "/edit", traits: "nav nostore signin visitor"},
	{pattern: "GET /account/cart-recovery", sample: "/account/cart-recovery", traits: "nav nostore signin visitor"},
	{pattern: "GET /account/orders/{number}", sample: "/account/orders/" + probeNumber, traits: "nav nostore signin visitor"},
	{pattern: "GET /account/points", sample: "/account/points", traits: "nav nostore signin visitor"},
	{pattern: "GET /account/warranty", sample: "/account/warranty", traits: "nav nostore signin visitor"},
	{pattern: "GET /account/warranty/{number}", sample: "/account/warranty/" + probeNumber, traits: "nav nostore signin visitor"},
	{pattern: "GET /account/wishlist", sample: "/account/wishlist", traits: "nav nostore signin visitor"},
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
	{pattern: "POST /account/addresses", sample: "/account/addresses", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/addresses/default", sample: "/account/addresses/default", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/addresses/delete", sample: "/account/addresses/delete", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/addresses/update", sample: "/account/addresses/update", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/cart/retry", sample: "/account/cart/retry", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/email", sample: "/account/email", traits: "limited nav nostore signin visitor"},
	{pattern: "POST /account/email/resend", sample: "/account/email/resend", traits: "limited nav nostore signin visitor"},
	{pattern: "POST /account/erase", sample: "/account/erase", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/google/unlink", sample: "/account/google/unlink", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/password", sample: "/account/password", traits: "limited nav nostore signin visitor"},
	{pattern: "POST /account/password/setup", sample: "/account/password/setup", traits: "limited nav nostore signin visitor"},
	{pattern: "POST /account/points", sample: "/account/points", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/profile", sample: "/account/profile", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/sessions/others/end", sample: "/account/sessions/others/end", traits: "nav nostore signin visitor"},
	{pattern: "POST /account/warranty/{number}", sample: "/account/warranty/" + probeNumber, traits: "nav nostore signin visitor"},
	{pattern: "POST /account/wishlist", sample: "/account/wishlist", traits: "nav nostore signin visitor"},
	{pattern: "POST /cart/items", sample: "/cart/items", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /cart/items/update", sample: "/cart/items/update", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /checkout", sample: "/checkout", traits: "limited nav nostore visitor"},
	{pattern: "POST /checkout/pickup/return", sample: "/checkout/pickup/return", traits: "nostore visitor"},
	{pattern: "POST /checkout/pickup/start", sample: "/checkout/pickup/start", traits: "limited nav nostore visitor"},
	{pattern: "POST /contact", sample: "/contact", traits: "limited nav nostore visitor"},
	{pattern: "POST /forgot", sample: "/forgot", traits: "limited nav nostore visitor"},
	{pattern: "POST /locale", sample: "/locale", traits: "nav nostore visitor"},
	{pattern: "POST /newsletter", sample: "/newsletter", traits: "limited nav nostore visitor"},
	{pattern: "POST /newsletter/confirm", sample: "/newsletter/confirm", traits: "nav nostore visitor"},
	{pattern: "POST /newsletter/unsubscribe", sample: "/newsletter/unsubscribe", traits: "nav nostore visitor"},
	{pattern: "POST /orders/find", sample: "/orders/find", traits: "limited nav nostore visitor"},
	{pattern: "POST /orders/{number}/cancel", sample: "/orders/" + probeNumber + "/cancel", traits: "nav nostore visitor"},
	{pattern: "POST /orders/{number}/pay", sample: "/orders/" + probeNumber + "/pay", traits: "nav nostore visitor"},
	{pattern: "POST /orders/{number}/reorder", sample: "/orders/" + probeNumber + "/reorder", traits: "clearsite nav nostore visitor"},
	{pattern: "POST /orders/{number}/return", sample: "/orders/" + probeNumber + "/return", traits: "nav nostore visitor"},
	{pattern: "POST /p/{slug}/notify", sample: "/p/" + probeSlug + "/notify", traits: "limited nav nostore visitor"},
	{pattern: "POST /p/{slug}/questions", sample: "/p/" + probeSlug + "/questions", traits: "nav nostore signin visitor"},
	{pattern: "POST /p/{slug}/reviews", sample: "/p/" + probeSlug + "/reviews", traits: "nav nostore signin visitor"},
	{pattern: "POST /promo/dismiss", sample: "/promo/dismiss", traits: "nav nostore visitor"},
	{pattern: "POST /register", sample: "/register", traits: "clearsite limited nav nostore visitor"},
	{pattern: "POST /register/complete", sample: "/register/complete", traits: "clearsite limited nav nostore visitor"},
	{pattern: "POST /register/resend", sample: "/register/resend", traits: "limited nav nostore visitor"},
	{pattern: "POST /reset", sample: "/reset", traits: "limited nav nostore visitor"},
	{pattern: "POST /signin", sample: "/signin", traits: "clearsite limited nav nostore visitor"},
	{pattern: "POST /signout", sample: "/signout", traits: "clearcache clearsite nav nostore visitor"},
	{pattern: "POST /verify", sample: "/verify", traits: "limited nav nostore visitor"},
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
// each row's sample reaches its own pattern rather than the catch-all.
func checkInventoryCoversTheRoutes(t *testing.T) {
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
	standalone := http.NewServeMux()
	for _, row := range storefrontInventory {
		standalone.HandleFunc(row.pattern, func(http.ResponseWriter, *http.Request) {})
	}
	for _, row := range storefrontInventory {
		if _, got := standalone.Handler(inventoryRequest(t, row, "inv-sample")); got != row.pattern {
			t.Errorf("%s: the sample %s reaches %q instead", row.pattern, row.sample, got)
		}
	}
}

// TestEveryStorefrontRouteHasAnInventoryRow fails the moment a storefront route
// is added, removed or renamed without its row: the walk's pattern count is
// compared with the table's, name by name, with no database needed.
func TestEveryStorefrontRouteHasAnInventoryRow(t *testing.T) {
	t.Parallel()

	checkInventoryCoversTheRoutes(t)
	if walked := len(storefrontPatterns(t)); walked != len(storefrontInventory) {
		t.Errorf("the walk found %d storefront patterns, the inventory has %d rows", walked, len(storefrontInventory))
	}
}
