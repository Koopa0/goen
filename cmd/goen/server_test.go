package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func TestPanicRecoveryUsesProductionRequestTracing(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	want := "return withRequestTracing(handler, log)"
	if !bytes.Contains(src, []byte(want)) {
		t.Fatalf("newRouter must return %q so panic recovery sees the request identifier", want)
	}
}

func TestNotifyRouteIsGuardedPerIP(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	want := `mux.HandleFunc("POST /p/{slug}/notify", ratelimit.Guard(notifyLimit, log, items.Notify))`
	if !bytes.Contains(src, []byte(want)) {
		t.Fatal("POST /p/{slug}/notify is registered without ratelimit.Guard")
	}
}

// TestCheckoutIsGuardedPerClient drives the router: every checkout post looks
// up the coupon it carries, so the route is bounded per client before the
// handler runs, generously enough that a shopper changing every chooser on the
// page is never refused.
func TestCheckoutIsGuardedPerClient(t *testing.T) {
	router := storeMapRouter(t, false)
	post := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout",
			strings.NewReader("coupon=GUESS"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}

	const shopper = 20
	for i := range shopper {
		if res := post("192.0.2.10:4000"); res.Code == http.StatusTooManyRequests {
			t.Fatalf("checkout post %d was refused; a shopper's whole checkout must fit", i+1)
		}
	}
	refused := false
	for range 60 {
		res := post("192.0.2.10:4000")
		if res.Code == http.StatusTooManyRequests {
			refused = true
			if res.Header().Get("Retry-After") == "" {
				t.Error("the refusal carries no Retry-After")
			}
			break
		}
	}
	if !refused {
		t.Fatalf("%d checkout posts from one client were never refused", shopper+60)
	}
	if res := post("192.0.2.11:4000"); res.Code == http.StatusTooManyRequests {
		t.Error("another client was refused; the bound is per client")
	}
}

// TestTextPostgreSQLCannotStoreIsRefusedBeforeRouting drives the router with
// the bytes no browser sends and PostgreSQL refuses to hold, in a query value,
// a query name and a path value, on the storefront and in the back office.
// Each is answered 400 before any handler reads it.
func TestTextPostgreSQLCannotStoreIsRefusedBeforeRouting(t *testing.T) {
	router := storeMapRouter(t, false)
	get := func(target string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))
		return res
	}

	for _, bad := range []string{"%E9", "%00", "a%C3%28b", "a%00b"} {
		for _, target := range []string{
			"/search?q=" + bad,
			"/search?" + bad + "=1",
			"/c/phones?brand=" + bad,
			"/c/" + bad,
			"/p/" + bad,
			"/s/" + bad,
			"/compare?p=" + bad,
			"/compare?p=aurora-slate&p=" + bad,
			"/admin/customers?q=" + bad,
			"/admin/orders?q=" + bad,
			"/admin/warranty?q=" + bad,
		} {
			res := get(target)
			if res.Code != http.StatusBadRequest {
				t.Errorf("GET %s answered %d, want 400", target, res.Code)
				continue
			}
			body := res.Body.String()
			if !strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") || !strings.Contains(body, `class="notice__actions"`) {
				t.Errorf("GET %s did not render the shop's 400 page", target)
			}
			_, actions, _ := strings.Cut(body, `class="notice__actions"`)
			actions, _, _ = strings.Cut(actions, "</div>")
			if !strings.Contains(actions, `href="/"`) {
				t.Errorf("GET %s has no shop recovery link", target)
			}
		}
	}

	// The control: text PostgreSQL stores, and a pair no reader decodes, pass.
	for _, target := range []string{
		"/search?q=%E6%89%8B%E6%A9%9F",
		"/search?q=phone&utm_content=50%off",
		"/c/phones?brand=koto",
	} {
		if res := get(target); res.Code == http.StatusBadRequest {
			t.Errorf("GET %s answered 400; only unstorable text is refused", target)
		}
	}
}

func TestAnAssetRequestNeverReachesPerVisitorMiddleware(t *testing.T) {
	tests := []struct {
		path    string
		wantRan int
	}{
		{path: "/static/css/app/app.css"},
		{path: "/static/css/app/app.css?v=abc"},
		{path: "/static/js/goen.js"},
		{path: "/media/0123abc"},
		{path: "/media/0123abc/400"},
		{path: "/healthz"},
		{path: "/readyz"},
		{path: "/webhooks/stripe"},
		{path: "/", wantRan: 1},
		{path: "/c/phones", wantRan: 1},
		{path: "/p/aurora-slate", wantRan: 1},
		{path: "/cart", wantRan: 1},
		{path: "/checkout", wantRan: 1},
		{path: "/account", wantRan: 1},
		{path: "/account/orders/G2601-0001", wantRan: 1},
		{path: "/signin", wantRan: 1},
		{path: "/orders/find", wantRan: 1},
		{path: "/admin", wantRan: 1},
		{path: "/admin/orders", wantRan: 1},
		{path: "/staticky", wantRan: 1},
		{path: "/mediation", wantRan: 1},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			var ran int
			counting := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ran++
					next.ServeHTTP(w, r)
				})
			}
			var served int
			h := onlyVisitorPaths(counting, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				served++
			}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if ran != tt.wantRan {
				t.Errorf("per-visitor middleware ran %d times, want %d", ran, tt.wantRan)
			}
			if served != 1 {
				t.Errorf("downstream handler ran %d times, want 1", served)
			}
		})
	}
}

func TestNothingStatelessRendersChrome(t *testing.T) {
	want := []string{"/static", "/media", "/healthz", "/readyz", "/webhooks", "/favicon.ico"}
	if !slices.Equal(statelessPrefixes, want) {
		t.Fatalf("statelessPrefixes = %v, want exactly %v", statelessPrefixes, want)
	}
	// Containment, not equality: /admin renders no storefront nav but must stay
	// visitor-aware because its authorisation reads the authenticated user.
	for _, prefix := range statelessPrefixes {
		if !slices.Contains(navFreePrefixes, prefix) {
			t.Errorf("stateless prefix %q is absent from navFreePrefixes", prefix)
		}
		if !slices.Contains(bannerFreePrefixes, prefix) {
			t.Errorf("stateless prefix %q is absent from bannerFreePrefixes", prefix)
		}
	}
}

func TestAnAssetIsNotCompressedTwiceByTheChain(t *testing.T) {
	h := web.Compress(securityHeaders(assets.Handler(slog.New(slog.DiscardHandler)), contentSecurityPolicy, false))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(assets.AppCSS), http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if got := res.Header().Values("Content-Encoding"); len(got) != 1 || got[0] != "gzip" {
		t.Fatalf("Content-Encoding values = %q, want exactly one gzip", got)
	}
	r, err := gzip.NewReader(bytes.NewReader(res.Body.Bytes()))
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close gzip response: %v", err)
	}
	if !bytes.Contains(body, []byte(".goen-header__bar")) {
		t.Error("one gunzip did not yield the application stylesheet")
	}
	if got := res.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", got)
	}
}

func TestTheStaticAssetHandlerOwnsItsEncodingDecision(t *testing.T) {
	body := bytes.Repeat([]byte("catalogue chose identity "), 200)
	asset := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	h := web.Compress(staticAssetHandler(asset))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/fixture.txt", http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want the asset handler's identity decision", got)
	}
	if got := res.Header().Get("X-Goen-No-Compress"); got != "" {
		t.Errorf("private compression marker leaked as %q", got)
	}
	if !bytes.Equal(res.Body.Bytes(), body) {
		t.Error("static identity body changed")
	}
}

func TestStatusRecorderKeepsTheFinalStatusAfterEarlyHints(t *testing.T) {
	underlying := &statusListWriter{header: make(http.Header)}
	recorder := &statusRecorder{ResponseWriter: underlying}
	recorder.WriteHeader(http.StatusEarlyHints)
	if got := recorder.statusCode(); got != http.StatusOK {
		t.Errorf("status after only early hints = %d, want default final 200", got)
	}
	recorder.WriteHeader(http.StatusOK)

	if want := []int{http.StatusEarlyHints, http.StatusOK}; !slices.Equal(underlying.statuses, want) {
		t.Errorf("forwarded statuses = %v, want %v", underlying.statuses, want)
	}
	if got := recorder.statusCode(); got != http.StatusOK {
		t.Errorf("recorded final status = %d, want 200", got)
	}
	recorder.WriteHeader(http.StatusEarlyHints)
	if want := []int{http.StatusEarlyHints, http.StatusOK}; !slices.Equal(underlying.statuses, want) {
		t.Errorf("statuses after final then 103 = %v, want %v", underlying.statuses, want)
	}

	switching := &statusRecorder{ResponseWriter: &statusListWriter{header: make(http.Header)}}
	switching.WriteHeader(http.StatusSwitchingProtocols)
	switching.WriteHeader(http.StatusOK)
	if got := switching.statusCode(); got != http.StatusSwitchingProtocols {
		t.Errorf("recorded status after 101 then 200 = %d, want final 101", got)
	}
}

// FuzzAssetAndDynamicGzipNegotiationAgree holds together the deliberately
// duplicated parsers on the package boundary: assets cannot import a feature
// package, but the two wire-format decisions must still be identical.
func FuzzAssetAndDynamicGzipNegotiationAgree(f *testing.F) {
	for _, seed := range [][2]string{
		{"gzip", ""},
		{"gzip;q=0", ""},
		{"*;q=1", "gzip;q=0"},
		{"br", "*;q=.4"},
		{"GZip; Q=.5", ""},
		{"gzip;q=wat", ""},
		{"gzip;q", ""},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, first, second string) {
		if len(first)+len(second) > 8<<10 {
			t.Skip()
		}
		setEncoding := func(req *http.Request) {
			req.Header.Add("Accept-Encoding", first)
			if second != "" {
				req.Header.Add("Accept-Encoding", second)
			}
		}

		assetReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			assets.URL(assets.AppCSS), http.NoBody)
		setEncoding(assetReq)
		assetOut := httptest.NewRecorder()
		assets.Handler(slog.New(slog.DiscardHandler)).ServeHTTP(assetOut, assetReq)

		dynamicReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		setEncoding(dynamicReq)
		dynamicOut := httptest.NewRecorder()
		web.Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(bytes.Repeat([]byte("x"), 2_000))
		})).ServeHTTP(dynamicOut, dynamicReq)

		assetGzip := assetOut.Header().Get("Content-Encoding") == "gzip"
		dynamicGzip := dynamicOut.Header().Get("Content-Encoding") == "gzip"
		if assetGzip != dynamicGzip {
			t.Errorf("Accept-Encoding %q + %q selected asset gzip=%v, dynamic gzip=%v",
				first, second, assetGzip, dynamicGzip)
		}
	})
}

// TestWebhookSurvivesTheMiddlewareChain proves Stripe can reach the webhook
// through goen's CSRF defence while a cross-site browser post still cannot.
func TestWebhookSurvivesTheMiddlewareChain(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantReached bool
	}{
		{
			name:        "as Stripe sends it: no browser headers at all",
			headers:     map[string]string{"Stripe-Signature": "t=1,v1=abc"},
			wantReached: true,
		},
		{
			name:        "a same-origin browser post is allowed",
			headers:     map[string]string{"Sec-Fetch-Site": "same-origin"},
			wantReached: true,
		},
		{
			name:        "a cross-site browser post is still refused",
			headers:     map[string]string{"Sec-Fetch-Site": "cross-site"},
			wantReached: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reached bool
			mux := http.NewServeMux()
			mux.HandleFunc("POST /webhooks/stripe", func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			handler := crossOriginProtection(mux, false)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/webhooks/stripe", strings.NewReader(`{"id":"evt_1"}`))
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if reached != tt.wantReached {
				t.Errorf("reached handler = %v, want %v (status %d)", reached, tt.wantReached, w.Code)
			}
		})
	}
}

// TestCSPAllowsTheHandoverToStripe proves the Content-Security-Policy still
// permits the redirect that sends a customer to Stripe's card form.
func TestCSPAllowsTheHandoverToStripe(t *testing.T) {
	var directive string
	for d := range strings.SplitSeq(contentSecurityPolicy, ";") {
		if strings.HasPrefix(strings.TrimSpace(d), "form-action") {
			directive = strings.TrimSpace(d)
		}
	}
	if directive == "" {
		t.Fatal("the policy has no form-action directive at all")
	}
	if !strings.Contains(directive, "https://checkout.stripe.com") {
		t.Errorf("form-action is %q; without checkout.stripe.com the redirect to "+
			"Stripe is blocked in browsers that apply form-action to redirects", directive)
	}
	if !strings.Contains(directive, "'self'") {
		t.Errorf("form-action is %q and no longer allows goen's own forms", directive)
	}
}

// inlineStyleAttribute matches a style attribute written into markup, in either
// of templ's two spellings: style="…" and style={ … }.
var inlineStyleAttribute = regexp.MustCompile(`(?:^|\s)style\s*=`)

// TestNoTemplateWritesAnInlineStyle holds contentSecurityPolicy's comment to its
// word. style-src carries no 'unsafe-inline', so a style attribute a template
// writes is dropped by the browser and the element renders without whatever the
// attribute carried: a rating bar with no width, a category tree with no indent.
// A handler test cannot see this — it reads the markup, not the policy that then
// refuses it — so the two statements are checked against each other here.
//
// The guard yields if the policy ever admits inline styles. That is one decision
// taken in server.go, not something a template may decide by drifting.
func TestNoTemplateWritesAnInlineStyle(t *testing.T) {
	t.Parallel()

	if strings.Contains(contentSecurityPolicy, "'unsafe-inline'") {
		t.Skip("style-src admits 'unsafe-inline'; a template may write a style attribute")
	}

	root := filepath.Join("..", "..", "internal", "ui")
	var scanned int
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || filepath.Ext(path) != ".templ" {
			return walkErr
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking a fixed directory
		if readErr != nil {
			return readErr
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment is prose about markup rather than markup.
			if strings.HasPrefix(trimmed, "//") || !inlineStyleAttribute.MatchString(line) {
				continue
			}
			offenders = append(offenders, "internal/ui/"+rel+":"+strconv.Itoa(i+1)+"  "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if scanned == 0 {
		t.Fatalf("no .templ files under %s; this guard has no subject", root)
	}

	if len(offenders) > 0 {
		t.Errorf("%d inline style attributes across %d templates, and style-src "+
			"refuses every one of them. Carry the value in a class or a data "+
			"attribute the stylesheet selects on, or take the decision in "+
			"server.go to admit 'unsafe-inline':\n%s",
			len(offenders), scanned, strings.Join(offenders, "\n"))
	}
}

// TestTheEchoedRequestIDIsTheOneOnTheContext binds the two ends of one
// identifier: the header a caller correlates on, and the context value the
// audit trail stamps onto a back-office row. They are one key, so a row and its
// log lines can be put together.
func TestTheEchoedRequestIDIsTheOneOnTheContext(t *testing.T) {
	tests := []struct {
		name     string
		supplied string
		want     string
	}{
		{name: "an id-shaped header is honoured", supplied: "abc-123", want: "abc-123"},
		{name: "one carrying a newline is replaced", supplied: "forged\nlog line"},
		{name: "an absent header is generated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var onContext string
			h := withRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				onContext = web.RequestID(r.Context())
			}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.supplied != "" {
				req.Header.Set("X-Request-Id", tt.supplied)
			}
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)

			echoed := res.Header().Get("X-Request-Id")
			switch {
			case echoed == "":
				t.Fatal("no X-Request-Id echoed")
			case tt.want != "" && echoed != tt.want:
				t.Errorf("echoed X-Request-Id = %q, want %q", echoed, tt.want)
			case tt.want == "" && echoed == tt.supplied:
				t.Errorf("echoed X-Request-Id = %q, want the supplied one replaced", echoed)
			}
			if onContext != echoed {
				t.Errorf("web.RequestID on the context = %q, echoed %q", onContext, echoed)
			}
		})
	}
}

type statusListWriter struct {
	header   http.Header
	statuses []int
}

func (w *statusListWriter) Header() http.Header { return w.header }

func (w *statusListWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}

func (w *statusListWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestOnlyAStaffMemberGetsTheBackOfficeEntrance holds the half the templates
// cannot see: which requests the chrome is told about at all. The three answers
// it has to keep apart are a customer (no door), a staff member on the
// storefront (the door), and a staff member already inside the back office,
// whose own adminNav row leads everywhere /admin goes.
func TestOnlyAStaffMemberGetsTheBackOfficeEntrance(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		role user.Role // "" is a signed-out visitor
		path string
		want bool
	}{
		{name: "signed out", path: "/", want: false},
		{name: "customer", role: user.RoleCustomer, path: "/", want: false},
		{name: "staff on the storefront", role: user.RoleStaff, path: "/", want: true},
		{name: "staff on a product page", role: user.RoleStaff, path: "/p/aurora-slate-11", want: true},
		{name: "admin on the account page", role: user.RoleAdmin, path: "/account", want: true},
		{name: "staff inside the back office", role: user.RoleStaff, path: "/admin/orders", want: false},
		{name: "staff on an asset", role: user.RoleStaff, path: "/static/js/goen.js", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got bool
			h := withStaffEntrance(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = layouts.IsStaff(r.Context())
			}))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			if tt.role != "" {
				req = req.WithContext(user.NewContext(req.Context(), user.User{
					ID: "user-1", Email: "somebody@example.com", Role: tt.role,
				}))
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if got != tt.want {
				t.Errorf("layouts.IsStaff on %s as %q = %v, want %v",
					tt.path, tt.role, got, tt.want)
			}
		})
	}
}

func TestLocaleReturnPathKeepsTheQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "root", raw: "/", want: "/"},
		{name: "deals", raw: "/deals", want: "/deals"},
		{name: "search keeps the term", raw: "/search?q=pixelight", want: "/search?q=pixelight"},
		{name: "listing keeps filters and sort", raw: "/c/phones?sort=price&brand=aurora", want: "/c/phones?sort=price&brand=aurora"},
		{name: "product keeps the chosen options", raw: "/p/aurora-slate?%E9%A1%8F%E8%89%B2=%E9%8A%80", want: "/p/aurora-slate?%E9%A1%8F%E8%89%B2=%E9%8A%80"},
		{name: "refused review returns to the product", raw: "/p/koto-over-ear/reviews", want: "/p/koto-over-ear"},
		{name: "refused question returns to the product", raw: "/p/koto-over-ear/questions", want: "/p/koto-over-ear"},
		{name: "a bad slug under reviews is left alone", raw: "/p/BAD_SLUG/reviews", want: "/p/BAD_SLUG/reviews"},
		{name: "compare keeps its products", raw: "/compare?p=aurora-charger-65&p=aurora-edge-7", want: "/compare?p=aurora-charger-65&p=aurora-edge-7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.raw, http.NoBody)
			if got := localeReturnPath(req); got != tt.want {
				t.Errorf("localeReturnPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLanguageSwitchComparisonJourney(t *testing.T) {
	t.Parallel()

	const rawURL = "/compare?p=aurora-charger-65&p=aurora-edge-7&q=search-leak"
	const wantReturn = rawURL

	var onContext string
	var renderedHTML string
	h := withLocale(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		onContext = web.RequestPath(r.Context())
		var buf bytes.Buffer
		if err := layouts.Footer(layouts.NewsletterState{}).Render(r.Context(), &buf); err != nil {
			t.Fatalf("render footer: %v", err)
		}
		renderedHTML = buf.String()
	}), false)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if onContext != wantReturn {
		t.Fatalf("web.RequestPath on context = %q, want %q", onContext, wantReturn)
	}

	re := regexp.MustCompile(`<input type="hidden" name="return" value="([^"]*)"`)
	matches := re.FindStringSubmatch(renderedHTML)
	if len(matches) < 2 {
		t.Fatalf("language form return input not found in rendered footer:\n%s", renderedHTML)
	}
	extractedReturn := html.UnescapeString(matches[1])
	if extractedReturn != wantReturn {
		t.Fatalf("rendered return target = %q, want %q", extractedReturn, wantReturn)
	}

	// Verify the return target resolves through the same-site redirect guard.
	redirectTarget := web.SitePathOr(extractedReturn, "/")
	if redirectTarget != wantReturn {
		t.Fatalf("SitePathOr(%q) = %q, want %q", extractedReturn, redirectTarget, wantReturn)
	}
}

// TestSpeculationRulesAreOfferedOnlyWhereTheyAreSafe locks both halves of the
// decision in #400: which pages offer speculation rules, and which never do.
//
// The rules document refuses to prerender a link into a personalised or
// paying page; this is the other half, and the reason there are two. A rule
// that stops matching is a silent failure — the browser simply prerenders
// something — so the pages where that would cost the most do not hand the
// browser a rules document at all.
func TestSpeculationRulesAreOfferedOnlyWhereTheyAreSafe(t *testing.T) {
	t.Parallel()

	offered := []string{"/", "/c/audio", "/p/nimbus-buds-pro", "/cart", "/about", "/search?q=x"}
	withheld := []string{
		"/checkout", "/checkout?ship=1",
		"/orders/find", "/orders/GO-1/pay",
		"/account", "/account/points", "/account/wishlist",
		"/admin", "/admin/orders",
		"/signin", "/register", "/register/complete?token=x", "/reset?token=x", "/verify?token=x",
		"/static/css/app/app.css",
	}

	for _, path := range offered {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		if !speculates(req) {
			t.Errorf("GET %s offers no speculation rules; it is a page a visitor "+
				"browses from and nothing it links to is personalised", path)
		}
	}
	for _, path := range withheld {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		if speculates(req) {
			t.Errorf("GET %s offers speculation rules; a page behind a sign-in or "+
				"inside paying must not hand the browser a rules document", path)
		}
	}
	// A write has already happened by the time it is answered.
	if speculates(httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/cart/items", http.NoBody)) {
		t.Error("a POST offers speculation rules")
	}
}

// TestSpeculationRulesRefuseEveryPageWithSomethingToLose reads the rules
// document itself rather than the middleware, because the document is what the
// browser obeys. Every path the middleware withholds the header from must also
// be one the rules refuse to prerender: the two guards are independent, and a
// link to /account can appear on a page that does offer rules — the header in
// every storefront page carries a rules document that sees that link.
func TestSpeculationRulesRefuseEveryPageWithSomethingToLose(t *testing.T) {
	t.Parallel()

	// From disk rather than through the assets package: the file is the thing
	// under review, and what it permits is a security decision that should be
	// read where a reviewer reads it. The same reason readCIWorkflow and the
	// axe baseline are read this way.
	path := filepath.Join("..", "..", "assets", assets.SpeculationRules)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a fixed path inside this repository
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Prerender []struct {
			Eagerness string          `json:"eagerness"`
			Where     json.RawMessage `json:"where"`
		} `json:"prerender"`
		Prefetch []json.RawMessage `json:"prefetch"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	if len(doc.Prerender) != 1 {
		t.Fatalf("%d prerender rule sets, want exactly one", len(doc.Prerender))
	}
	if len(doc.Prefetch) != 0 {
		t.Errorf("%d prefetch rule sets; #400 was ruled to stop at prerendering "+
			"the catalogue, and a prefetch of a personalised page has the same "+
			"staleness to lose as a prerender", len(doc.Prefetch))
	}
	if doc.Prerender[0].Eagerness != "moderate" {
		t.Errorf("eagerness is %q, want moderate: eager speculation spends a "+
			"visitor's data on pages they did not ask for",
			doc.Prerender[0].Eagerness)
	}
	where := string(doc.Prerender[0].Where)
	for _, refused := range []string{
		"/checkout*", "/orders/*", "/account*", "/admin*", "/signin*", "/register*",
	} {
		if !strings.Contains(where, `"href_matches": "`+refused+`"`) {
			t.Errorf("the rules do not refuse %s; a link to it on any storefront "+
				"page would be prerendered", refused)
		}
	}

	// The cart is NOT prerendered, and that is a measurement rather than a
	// preference. GET /cart writes nothing, which was the test the ruling set —
	// but its content depends on the cart, and since #415 adding an item does
	// not navigate. So the browser can take a copy of the cart page while the
	// cart is empty, the visitor can add an item without leaving the product
	// page, and pressing the cart link then shows the copy: no lines, and a
	// header count back at zero. Measured both ways at 1440, one rule apart.
	//
	// Answering /cart with Cache-Control: no-store does not help; the copy
	// lives in the speculation cache, not the HTTP one. The only fix is to not
	// take it.
	if strings.Contains(where, `"href_matches": "/cart"`) {
		t.Error("the rules prerender /cart; a copy taken before an in-place add " +
			"is served after it, and the visitor sees an empty cart")
	}
}

// TestAWriteThrowsAwaySpeculationsTakenBeforeIt pins the other half of #400.
//
// A speculated page is a whole document, header included, rendered when the
// browser asked for it. goen renders the cart's count and the staff/customer
// entrance into that header, so a copy taken before a write shows what the
// header said before the write — measured at 1440: speculate a related product,
// add in place so the count goes 1 to 2, press the link, and the landed page's
// header reads 1. With the header it reads 2.
//
// The absence half matters as much: Clear-Site-Data on a page response would
// throw away the speculations the visitor's own browsing just earned, which is
// the feature paying for itself and then refunding it.
func TestAWriteThrowsAwaySpeculationsTakenBeforeIt(t *testing.T) {
	t.Parallel()

	const want = `"prefetchCache", "prerenderCache"`
	sawWrite := false
	handler := clearSpeculations(func(http.ResponseWriter, *http.Request) { sawWrite = true })

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), method, "/cart/items", http.NoBody)
		handler(res, req)
		if got := res.Header().Get("Clear-Site-Data"); got != want {
			t.Errorf("%s Clear-Site-Data = %q, want %q", method, got, want)
		}
	}
	if !sawWrite {
		t.Error("the wrapped handler never ran; the middleware swallowed the request")
	}

	// The value, and not only the header's presence. Clear-Site-Data's other
	// directives are destructive in a way these two are not: "cookies" on an
	// add-to-cart response signs the shopper out and empties the cart it was
	// meant to protect, "storage" and "cache" throw away work the visitor's
	// browsing paid for, and "*" does all of it. Widening this value is a
	// plausible edit — it reads like making the header more thorough — so the
	// four spellings that must never appear are named here.
	res := httptest.NewRecorder()
	handler(res, httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/cart/items", http.NoBody))
	value := res.Header().Get("Clear-Site-Data")
	for _, forbidden := range []string{"cookies", "storage", "cache", "*"} {
		// "cache" is a substring of nothing here: the two permitted directives
		// are prefetchCache and prerenderCache, so a case-sensitive search for
		// the lower-case directive name cannot match either.
		if strings.Contains(value, `"`+forbidden+`"`) || value == forbidden {
			t.Errorf("Clear-Site-Data is %q and carries %q: on a cart write that "+
				"signs the shopper out, empties what they were saving, or throws "+
				"away the browsing that paid for the speculation", value, forbidden)
		}
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		res := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), method, "/p/x", http.NoBody)
		handler(res, req)
		if got := res.Header().Get("Clear-Site-Data"); got != "" {
			t.Errorf("%s carries Clear-Site-Data = %q; a page response must not "+
				"throw away the visitor's own speculations", method, got)
		}
	}
}

// TestEveryWriteThatChangesTheChromeClearsSpeculations reads server.go and
// names the routes, because the wiring is the part that rots: a new cart write
// added beside the others inherits nothing, and the failure is invisible — a
// count one behind on one page view.
func TestEveryWriteThatChangesTheChromeClearsSpeculations(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	text := string(src)

	// The header renders the cart's count and the staff/customer entrance.
	// These are the writes that move either one.
	for _, route := range []string{
		`"POST /cart/items"`,
		`"POST /cart/items/update"`,
		`"POST /orders/{number}/reorder"`,
		`"POST /signin"`,
		`"POST /signout"`,
		`"POST /register"`,
		`"POST /register/complete"`,
	} {
		line := routeLine(text, route)
		if line == "" {
			t.Errorf("no route registered for %s; this test's inventory has gone stale", route)
			continue
		}
		if !strings.Contains(line, "clearSpeculations(") {
			t.Errorf("%s does not clear speculations: a page speculated before it "+
				"keeps the header it was rendered with\n  %s", route, strings.TrimSpace(line))
		}
	}
}

// routeLine returns the mux registration line naming route, or "".
func routeLine(src, route string) string {
	for line := range strings.SplitSeq(src, "\n") {
		if strings.Contains(line, "mux.HandleFunc(") && strings.Contains(line, route) {
			return line
		}
	}
	return ""
}

// TestTheCatalogueStaysEligibleForSpeculation is the inverse of the refusals,
// and it exists because the first version of the rules carried a
// selector_matches of "form a" as a precaution — and a listing's compare form
// wraps the whole tile grid, so that one line silently excluded every product
// link on the site. The feature was doing nothing and every other test passed.
func TestTheCatalogueStaysEligibleForSpeculation(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "assets", assets.SpeculationRules)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a fixed path inside this repository
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Prerender []struct {
			Where struct {
				And []map[string]json.RawMessage `json:"and"`
			} `json:"where"`
		} `json:"prerender"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	if len(doc.Prerender) != 1 {
		t.Fatalf("%d prerender rule sets, want one", len(doc.Prerender))
	}

	var allowed, refusals int
	for _, clause := range doc.Prerender[0].Where.And {
		if or, ok := clause["or"]; ok {
			var patterns []struct {
				HrefMatches string `json:"href_matches"`
			}
			if err := json.Unmarshal(or, &patterns); err != nil {
				t.Fatalf("the or clause does not parse: %v", err)
			}
			for _, p := range patterns {
				if p.HrefMatches == "/p/*" || p.HrefMatches == "/c/*" {
					allowed++
				}
			}
		}
		if _, ok := clause["not"]; ok {
			refusals++
		}
	}
	if allowed != 2 {
		t.Errorf("the rules permit %d of the two catalogue shapes; a product link "+
			"that matches nothing is a feature that quietly does nothing", allowed)
	}
	// A refusal that matches a catalogue link would do the same damage as the
	// "form a" selector did. Anything that is not an href_matches of a path
	// outside /p/ and /c/, or a nofollow selector, needs a reason in review.
	if refusals == 0 {
		t.Error("the rules refuse nothing; the exclusions have been lost")
	}
}

func TestSiteOriginReachesTheChromeFromTheConfiguredBaseURL(t *testing.T) {
	t.Parallel()
	var got, path string
	h := withSiteOrigin(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = layouts.SiteOrigin(r.Context())
		path = layouts.RequestPath(r.Context())
	}), "https://shop.example/")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://evil.example/c/books?page=2", http.NoBody))
	if got != "https://shop.example" {
		t.Errorf("origin = %q, want the configured one and never the request's Host", got)
	}
	if path != "/c/books" {
		t.Errorf("path = %q, want the request's path without its query", path)
	}
}

// TestNoPageForOneVisitorIsKeptByTheBrowser holds both halves of withNoStore.
// A page kept in the back/forward cache comes back with the Back button after
// its owner has signed out, so the next person at a shared computer reads an
// account, a customer list or a second-factor seed, or finds a signed-out
// visitor's sign-in, registration or password form holding what was typed
// into it. The anonymous catalogue is the other half: storing nothing there
// would cost every shopper an instant Back and protect nobody.
func TestNoPageForOneVisitorIsKeptByTheBrowser(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		method   string
		path     string
		signedIn bool
		want     bool
	}{
		{name: "the account page", method: http.MethodGet, path: "/account", signedIn: true, want: true},
		{name: "an account order", method: http.MethodGet, path: "/account/orders/G2601-0001", signedIn: true, want: true},
		{name: "the back office", method: http.MethodGet, path: "/admin/orders", signedIn: true, want: true},
		{name: "a customer search", method: http.MethodGet, path: "/admin/customers?q=chen", signedIn: true, want: true},
		{name: "the second-factor seed", method: http.MethodPost, path: "/admin/verify/enrol", signedIn: true, want: true},
		{name: "a product page, signed in", method: http.MethodGet, path: "/p/aurora-slate", signedIn: true, want: true},
		{name: "the home page, signed in", method: http.MethodGet, path: "/", signedIn: true, want: true},
		{name: "an order opened by its link", method: http.MethodGet, path: "/orders/G2601-0001?token=x", want: true},
		{name: "an order's pay page", method: http.MethodGet, path: "/orders/G2601-0001/pay", want: true},
		{name: "the cart", method: http.MethodGet, path: "/cart", want: true},
		{name: "the checkout", method: http.MethodGet, path: "/checkout", want: true},
		{name: "a password reset link", method: http.MethodGet, path: "/reset?token=x", want: true},
		{name: "an address verification link", method: http.MethodGet, path: "/verify?token=x", want: true},
		{name: "a registration link", method: http.MethodGet, path: "/register/complete?token=x", want: true},
		{name: "a newsletter confirmation link", method: http.MethodGet, path: "/newsletter/confirm?token=x", want: true},
		{name: "the sign-in form", method: http.MethodGet, path: "/signin", want: true},
		{name: "the registration form", method: http.MethodGet, path: "/register?next=/cart", want: true},
		{name: "the forgotten-password form", method: http.MethodGet, path: "/forgot", want: true},
		{name: "a refused form's answer", method: http.MethodPost, path: "/contact", want: true},
		{name: "the home page", method: http.MethodGet, path: "/", want: false},
		{name: "a product page", method: http.MethodGet, path: "/p/aurora-slate", want: false},
		{name: "a listing", method: http.MethodGet, path: "/c/phones", want: false},
		{name: "a search", method: http.MethodGet, path: "/search?q=x", want: false},
		{name: "the returns policy", method: http.MethodGet, path: "/returns", want: false},
		{name: "the payment policy", method: http.MethodGet, path: "/payment", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := withNoStore(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody)
			if tt.signedIn {
				req = req.WithContext(user.NewContext(req.Context(), user.User{
					ID: "user-1", Email: "somebody@example.com", Role: user.RoleAdmin,
				}))
			}
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)

			got := res.Header().Get("Cache-Control")
			switch {
			case tt.want && got != "no-store":
				t.Errorf("%s %s signedIn=%v: Cache-Control = %q, want no-store: the Back "+
					"button brings the page back after sign-out", tt.method, tt.path, tt.signedIn, got)
			case !tt.want && got != "":
				t.Errorf("%s %s anonymous: Cache-Control = %q, want none: a public page "+
					"loses its instant Back and protects nobody", tt.method, tt.path, got)
			}
		})
	}
}

// TestOnlyTheSecurePosturePinsHTTPS drives the real router in both postures.
// With secure cookies goen is behind TLS, and HSTS stops a network that rewrites
// an http:// link from keeping a visitor in cleartext. Without them it serves
// http://127.0.0.1, where the header would pin a developer's localhost to https.
func TestOnlyTheSecurePosturePinsHTTPS(t *testing.T) {
	t.Parallel()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}

	for _, tt := range []struct {
		secure bool
		want   string
	}{
		{secure: true, want: "max-age=31536000; includeSubDomains"},
		{secure: false, want: ""},
	} {
		router := newRouter(&RouterConfig{
			Storefront: StorefrontConfig{
				StorePool: idle, Payments: gateway, BaseURL: "https://goen.test", SecureCookies: tt.secure,
			},
			BackOffice: BackOfficeConfig{
				AdminPool: idle, Payments: gateway, Refunder: refunds.NewRefunder(""), SecureCookies: tt.secure,
			},
		}, slog.New(slog.DiscardHandler))
		// A probe reaches no database, so the idle pool answers for nothing.
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody))
		if res.Code != http.StatusOK {
			t.Fatalf("secure=%v: /healthz answered %d", tt.secure, res.Code)
		}
		if got := res.Header().Get("Strict-Transport-Security"); got != tt.want {
			t.Errorf("secure=%v: Strict-Transport-Security = %q, want %q", tt.secure, got, tt.want)
		}
	}
}

// TestSharePreviewNamesTheConfiguredOriginNotTheRequestHost pins withSiteOrigin
// in the router: a share preview's absolute URL is one a client must not be
// able to choose by sending a Host header.
func TestSharePreviewNamesTheConfiguredOriginNotTheRequestHost(t *testing.T) {
	t.Parallel()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: idle, Payments: gateway, BaseURL: "https://goen.test",
		},
		BackOffice: BackOfficeConfig{
			AdminPool: idle, Payments: gateway, Refunder: refunds.NewRefunder(""),
		},
	}, slog.New(slog.DiscardHandler))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/about", http.NoBody)
	req.Host = "evil.example"
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	body := res.Body.String()
	if !strings.Contains(body, `<meta property="og:image" content="https://goen.test/`) {
		t.Errorf("/about (status %d) does not name the configured origin in og:image", res.Code)
	}
	if strings.Contains(body, "evil.example") {
		t.Error("/about echoes the request's Host into the page")
	}
}

// TestSigningOutEmptiesTheBrowsersCache pins the second line behind
// withNoStore: sign-out asks the browser to drop what it holds for this site,
// and still throws away the speculations the chrome was rendered into.
func TestSigningOutEmptiesTheBrowsersCache(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	line := routeLine(string(src), `"POST /signout"`)
	if !strings.Contains(line, "clearCache(") {
		t.Errorf("POST /signout does not empty the browser's cache\n  %s", strings.TrimSpace(line))
	}

	signOut := clearSpeculations(clearCache(func(http.ResponseWriter, *http.Request) {}))
	res := httptest.NewRecorder()
	signOut(res, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signout", http.NoBody))
	value := res.Header().Get("Clear-Site-Data")
	for _, want := range []string{`"cache"`, `"prefetchCache"`, `"prerenderCache"`} {
		if !strings.Contains(value, want) {
			t.Errorf("sign-out Clear-Site-Data = %q, want it to carry %s", value, want)
		}
	}
	// Sign-out clears its own session cookie; "cookies" would also drop the
	// visitor's language and cart, and "storage" and "*" reach further still.
	for _, forbidden := range []string{`"cookies"`, `"storage"`, `"*"`} {
		if strings.Contains(value, forbidden) {
			t.Errorf("sign-out Clear-Site-Data = %q carries %s", value, forbidden)
		}
	}
}
