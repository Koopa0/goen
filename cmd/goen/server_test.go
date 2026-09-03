package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/web"
)

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
	want := []string{"/static", "/media", "/healthz", "/readyz", "/webhooks"}
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
	h := web.Compress(securityHeaders(assets.Handler(slog.New(slog.DiscardHandler))))
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
			handler := crossOriginProtection(mux)

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
