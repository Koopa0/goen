package assets_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
)

var testLog = slog.New(slog.DiscardHandler)

type requestMarkerKey struct{}

type contextLogHandler struct {
	contextValue string
	record       slog.Record
}

func (*contextLogHandler) Enabled(context.Context, slog.Level) bool { return true }

//nolint:gocritic // slog.Handler fixes Record as a value parameter.
func (h *contextLogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.contextValue, _ = ctx.Value(requestMarkerKey{}).(string)
	h.record = record.Clone()
	return nil
}

func (h *contextLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *contextLogHandler) WithGroup(string) slog.Handler      { return h }

type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header { return w.header }
func (*failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("client stopped reading")
}
func (*failingResponseWriter) WriteHeader(int) {}

// TestURLRequiredAssetsVersioned proves each constant produces the versioned
// URL the year-long cache header depends on.
func TestURLRequiredAssetsVersioned(t *testing.T) {
	t.Parallel()

	names := []string{
		assets.BaseCSS,
		assets.AppCSS,
		assets.AdminCSS,
		assets.HTMXJS,
		assets.AppJS,
		assets.TransitionsJS,
		assets.MarkSVG,
		assets.HomeHeroImage,
		assets.HomeHeroImage720,
		assets.EmailHeader,
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := assets.URL(name)
			prefix := "/static/" + name + "?v="
			if !strings.HasPrefix(got, prefix) {
				t.Fatalf("URL(%q) = %q; want prefix %q", name, got, prefix)
			}
			if digest := strings.TrimPrefix(got, prefix); len(digest) != 12 {
				t.Errorf("URL(%q) digest = %q; want 12 characters", name, digest)
			}
		})
	}
}

// TestURLUnknownAsset checks that the URL of an unknown asset is not versioned.
func TestURLUnknownAsset(t *testing.T) {
	t.Parallel()

	got := assets.URL("css/does-not-exist.css")
	if strings.Contains(got, "?v=") {
		t.Errorf("URL() = %q; an unknown asset must not claim a version", got)
	}
}

func TestHandlerServesRequestedAsset(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(assets.AppCSS), http.NoBody)
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	// An independent literal from the stylesheet, not a symbol the handler
	// shares, so a handler that served the wrong file still fails here.
	if !strings.Contains(res.Body.String(), ".goen-header__bar") {
		t.Error("response body is not the application stylesheet")
	}
	if got := res.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q; want the immutable long cache", got)
	}
}

// TestHandlerWriteFailureLogging checks that a write failure is logged through the
// configured logger, with the request's context.
func TestHandlerWriteFailureLogging(t *testing.T) {
	logHandler := &contextLogHandler{}
	log := slog.New(logHandler)
	ctx := context.WithValue(t.Context(), requestMarkerKey{}, "asset-request")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, assets.URL(assets.AppCSS), http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	res := &failingResponseWriter{header: make(http.Header)}

	assets.Handler(log).ServeHTTP(res, req)

	if logHandler.contextValue != "asset-request" {
		t.Errorf("write warning context marker = %q, want %q", logHandler.contextValue, "asset-request")
	}
	// One message for both representations this package writes from memory:
	// the gzip one and a stylesheet whose remote @import was removed. Which it
	// was is in the record's name field, not in the sentence.
	if logHandler.record.Message != "assets: write body" {
		t.Errorf("write warning message = %q, want %q",
			logHandler.record.Message, "assets: write body")
	}
}

// TestHandlerGzipPrecompressed checks that an asset is served precompressed to a
// client that accepts gzip.
func TestHandlerGzipPrecompressed(t *testing.T) {
	t.Parallel()

	res := requestAsset(t, assets.AppCSS, "gzip", "")
	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", got)
	}
	if !variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q, want Accept-Encoding", res.Header().Values("Vary"))
	}
	if got := res.Header().Get("Content-Length"); got != strconv.Itoa(res.Body.Len()) {
		t.Errorf("Content-Length = %q, want %d", got, res.Body.Len())
	}
	if got := res.Header().Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges = %q, want absent on the gzip representation", got)
	}
	if body := gunzipAsset(t, res.Body.Bytes()); !bytes.Contains(body, []byte(".goen-header__bar")) {
		t.Error("gunzipped response is not the application stylesheet")
	}
}

// TestHandlerSmallAssetIdentityOnly checks that a small text asset has only its
// identity representation: no gzip, no Vary.
func TestHandlerSmallAssetIdentityOnly(t *testing.T) {
	t.Parallel()

	res := requestAsset(t, assets.MarkSVG, "gzip", "")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity below the precompression threshold", got)
	}
	if variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q; a small asset has no gzip representation", res.Header().Values("Vary"))
	}
	if res.Body.Len() >= 1024 {
		t.Fatalf("fixture grew to %d bytes; it no longer proves the small-asset branch", res.Body.Len())
	}
}

// TestHandlerNoAcceptEncodingIdentity checks that an asset requested without
// Accept-Encoding is served as identity.
func TestHandlerNoAcceptEncodingIdentity(t *testing.T) {
	t.Parallel()

	res := requestAsset(t, assets.AppCSS, "", "")
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity", got)
	}
	if got := res.Header().Get("Content-Length"); got != strconv.Itoa(res.Body.Len()) {
		t.Errorf("Content-Length = %q, want %d", got, res.Body.Len())
	}
	if !strings.Contains(res.Body.String(), ".goen-header__bar") {
		t.Error("identity response is not the application stylesheet")
	}
	if !variesOnAcceptEncoding(res.Header()) {
		t.Errorf("Vary = %q, want Accept-Encoding on the identity representation", res.Header().Values("Vary"))
	}
}

// TestHandlerGzipNegotiation checks that gzip negotiation honours quality values
// and the wildcard.
func TestHandlerGzipNegotiation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "q zero", header: "gzip;q=0", want: false},
		{name: "explicit zero beats wildcard", header: "*;q=1, gzip;q=0", want: false},
		{name: "wildcard", header: "br, *;q=0.4", want: true},
		{name: "case insensitive", header: "GZip; Q=.5", want: true},
		{name: "invalid quality", header: "gzip;q=wat", want: false},
		{name: "missing quality value", header: "gzip;q", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := requestAsset(t, assets.AppCSS, tt.header, "")
			if got := res.Header().Get("Content-Encoding") == "gzip"; got != tt.want {
				t.Errorf("gzip selected = %v, want %v (Content-Encoding %q)",
					got, tt.want, res.Header().Get("Content-Encoding"))
			}
		})
	}
}

// TestHandlerImageNotPrecompressed checks that an image is not precompressed: its
// bytes do not change when the client advertises gzip.
func TestHandlerImageNotPrecompressed(t *testing.T) {
	t.Parallel()

	identity := requestAsset(t, assets.HomeHeroImage, "", "")
	negotiated := requestAsset(t, assets.HomeHeroImage, "gzip", "")
	if got := negotiated.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity for webp", got)
	}
	if variesOnAcceptEncoding(negotiated.Header()) {
		t.Errorf("Vary = %q; an image with one representation must not fragment the cache",
			negotiated.Header().Values("Vary"))
	}
	if !bytes.Equal(negotiated.Body.Bytes(), identity.Body.Bytes()) {
		t.Error("image bytes changed when the client advertised gzip")
	}
}

// TestHandlerETagPerEncoding checks that an asset's ETag distinguishes its
// encodings: each validator answers 304 for its own representation only.
func TestHandlerETagPerEncoding(t *testing.T) {
	t.Parallel()

	identity := requestAsset(t, assets.AppCSS, "", "")
	compressed := requestAsset(t, assets.AppCSS, "gzip", "")
	identityTag := identity.Header().Get("ETag")
	gzipTag := compressed.Header().Get("ETag")
	if identityTag == "" || gzipTag == "" || identityTag == gzipTag {
		t.Fatalf("identity ETag = %q, gzip ETag = %q; want two validators", identityTag, gzipTag)
	}
	identity304 := requestAsset(t, assets.AppCSS, "", identityTag)
	if identity304.Code != http.StatusNotModified || identity304.Body.Len() != 0 {
		t.Errorf("identity validator got status %d, body %d; want 304 and empty", identity304.Code, identity304.Body.Len())
	}
	if got := identity304.Header().Get("ETag"); got != identityTag {
		t.Errorf("identity 304 ETag = %q, want %q", got, identityTag)
	}
	if got := identity304.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("identity 304 Content-Encoding = %q, want identity", got)
	}
	if !variesOnAcceptEncoding(identity304.Header()) {
		t.Errorf("identity 304 Vary = %q, want Accept-Encoding", identity304.Header().Values("Vary"))
	}
	if got := identity304.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("identity 304 Cache-Control = %q, want immutable", got)
	}

	gzip304 := requestAsset(t, assets.AppCSS, "gzip", gzipTag)
	if gzip304.Code != http.StatusNotModified || gzip304.Body.Len() != 0 {
		t.Errorf("gzip validator got status %d, body %d; want 304 and empty", gzip304.Code, gzip304.Body.Len())
	}
	if got := gzip304.Header().Get("ETag"); got != gzipTag {
		t.Errorf("gzip 304 ETag = %q, want %q", got, gzipTag)
	}
	if got := gzip304.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("gzip 304 Content-Encoding = %q, want gzip", got)
	}
	if !variesOnAcceptEncoding(gzip304.Header()) {
		t.Errorf("gzip 304 Vary = %q, want Accept-Encoding", gzip304.Header().Values("Vary"))
	}
	if got := gzip304.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("gzip 304 Cache-Control = %q, want immutable", got)
	}
	if got := requestAsset(t, assets.AppCSS, "", gzipTag); got.Code != http.StatusOK {
		t.Errorf("gzip validator on identity got status %d, want 200", got.Code)
	}
}

// TestHandlerLaterIfNoneMatchLine checks that an asset matches a validator that
// arrives on a later If-None-Match header line.
func TestHandlerLaterIfNoneMatchLine(t *testing.T) {
	compressed := requestAsset(t, assets.AppCSS, "gzip", "")
	etag := compressed.Header().Get("ETag")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(assets.AppCSS), http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Add("If-None-Match", `"unrelated"`)
	req.Header.Add("If-None-Match", "W/"+etag)
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)
	if res.Code != http.StatusNotModified || res.Body.Len() != 0 {
		t.Errorf("later weak validator got status %d, body %d; want 304 and empty", res.Code, res.Body.Len())
	}
}

// TestHandlerHeadRequest checks that a HEAD request carries the headers of the
// representation a GET selects, and no body.
func TestHandlerHeadRequest(t *testing.T) {
	t.Parallel()

	get := requestAsset(t, assets.AppCSS, "gzip", "")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodHead, assets.URL(assets.AppCSS), http.NoBody)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.Len() != 0 {
		t.Errorf("HEAD status=%d body=%d, want 200 and empty", res.Code, res.Body.Len())
	}
	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header().Get("Content-Length"); got != strconv.Itoa(get.Body.Len()) {
		t.Errorf("HEAD Content-Length = %q, want gzip GET length %d", got, get.Body.Len())
	}
	for _, name := range []string{"Content-Type", "ETag", "Cache-Control"} {
		if got, want := res.Header().Get(name), get.Header().Get(name); got != want {
			t.Errorf("HEAD %s = %q, want gzip GET value %q", name, got, want)
		}
	}
	if !variesOnAcceptEncoding(res.Header()) {
		t.Errorf("HEAD Vary = %q, want Accept-Encoding", res.Header().Values("Vary"))
	}
	if got := res.Header().Get("Accept-Ranges"); got != "" {
		t.Errorf("HEAD Accept-Ranges = %q, want absent", got)
	}
}

// TestHandlerLongCacheNeedsVersion checks that Handler refuses the long cache to a
// request without the matching version, whether it has none or a stale one.
func TestHandlerLongCacheNeedsVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "no version", path: "/static/" + assets.AppCSS},
		{name: "stale version", path: "/static/" + assets.AppCSS + "?v=000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			res := httptest.NewRecorder()
			assets.Handler(testLog).ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", res.Code)
			}
			if got := res.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q; want %q", got, "no-cache")
			}
		})
	}
}

// TestAppCSSPageTransition checks that the stylesheet carries the page transition.
// It proves the rules that turn a navigation into a cross-fade reach the browser.
// They are the whole of #369 — CSS and nothing else — so without this assertion
// the at-rule could be dropped and every other test here would stay green while
// every page went back to a hard cut.
func TestAppCSSPageTransition(t *testing.T) {
	t.Parallel()

	res := requestAsset(t, assets.AppCSS, "", "")
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", assets.AppCSS, res.Code, http.StatusOK)
	}

	sheet := res.Body.String()
	// The opt-in, then each name that keeps a shared region from repainting
	// between the two documents.
	for _, rule := range []string{
		"@view-transition {\n  navigation: auto;\n  types: page;\n}",
		"view-transition-name: header;",
		"view-transition-name: footer;",
		"view-transition-name: gallery;",
		"view-transition-name: buybox;",
		"html:active-view-transition-type(page)::view-transition-group(*.photo) {\n  animation-duration: var(--dur-move);",
		"html:active-view-transition-type(page)::view-transition-new(*) {\n  animation-duration: var(--dur-slow);",
	} {
		if !strings.Contains(sheet, rule) {
			t.Errorf("served %s does not contain %q", assets.AppCSS, rule)
		}
	}
}

func requestAsset(t *testing.T, name, acceptEncoding, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(name), http.NoBody)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)
	return res
}

func gunzipAsset(t *testing.T, body []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close gzip response: %v", err)
	}
	return got
}

func variesOnAcceptEncoding(h http.Header) bool {
	for _, line := range h.Values("Vary") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Accept-Encoding") {
				return true
			}
		}
	}
	return false
}

// TestAppJSDemoAccountSignsIn checks that the demo account button signs in where
// the script runs. The button is labelled as signing in, so the script that
// reveals it must also submit the form it fills.
func TestAppJSDemoAccountSignsIn(t *testing.T) {
	t.Parallel()

	script := requestAsset(t, assets.AppJS, "", "").Body.String()
	start := strings.Index(script, "function demoAccount()")
	if start < 0 {
		t.Fatalf("served %s has no demoAccount function", assets.AppJS)
	}
	body := script[start:]
	if end := strings.Index(body, "\n  }\n"); end > 0 {
		body = body[:end]
	}
	for _, want := range []string{"[data-demo-fill]", "fill.hidden = false", `"email", "password"`, "form.requestSubmit()"} {
		if !strings.Contains(body, want) {
			t.Errorf("served %s's demoAccount does not contain %q", assets.AppJS, want)
		}
	}
}

// TestAppJSCarouselNoAutoplay checks that the carousel moves only on the visitor's
// input. Nothing starts by itself, so the script holds no timer, no animation hook
// and no autoplay switch (WCAG 2.2.2).
func TestAppJSCarouselNoAutoplay(t *testing.T) {
	t.Parallel()

	script := requestAsset(t, assets.AppJS, "", "").Body.String()
	start := strings.Index(script, "function carousel()")
	if start < 0 {
		t.Fatalf("served %s has no carousel function", assets.AppJS)
	}
	body := script[start:]
	if end := strings.Index(body, "\n  }\n"); end > 0 {
		body = body[:end]
	}
	for _, banned := range []string{"setInterval", "setTimeout", "requestAnimationFrame", "animationend", "autoplay"} {
		if strings.Contains(body, banned) {
			t.Errorf("served %s's carousel contains %q: a slide would move without the visitor", assets.AppJS, banned)
		}
	}
	sheet := requestAsset(t, assets.AppCSS, "", "").Body.String()
	if strings.Contains(sheet, "goen-hero-progress") {
		t.Errorf("served %s still draws the carousel's autoplay progress", assets.AppCSS)
	}
}

// TestAppJSCartQuantityAutosubmit checks that a cart quantity applies itself where
// the script runs. It holds the two halves of one promise: the script submits a
// quantity form on change, and the stylesheet retires the 更新 button only for a
// browser that runs it. Dropping either leaves a cart a shopper cannot update, or
// one with a button that does nothing.
func TestAppJSCartQuantityAutosubmit(t *testing.T) {
	t.Parallel()

	script := requestAsset(t, assets.AppJS, "", "").Body.String()
	for _, want := range []string{"ctx.transition = false", "htmx:finally:request", `window.location.assign("/cart")`} {
		if !strings.Contains(script, want) {
			t.Errorf("served %s does not contain %q, so a cart update could cross-fade the page or strand a refused quantity", assets.AppJS, want)
		}
	}
	if !strings.Contains(script, "form[data-autosubmit]") || !strings.Contains(script, "requestSubmit") {
		t.Errorf("served %s does not submit a data-autosubmit form on change", assets.AppJS)
	}

	sheet := requestAsset(t, assets.AppCSS, "", "").Body.String()
	if !strings.Contains(sheet, "@media (scripting: enabled) {\n  .goen-line__update {\n    display: none;") {
		t.Errorf("served %s does not hide .goen-line__update under scripting: enabled", assets.AppCSS)
	}
}

// TestHandlerNotFound checks that Handler refuses unknown files and directories
// with 404.
func TestHandlerNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "unknown file", path: "/static/css/nope.css"},
		{name: "directory listing", path: "/static/css/"},
		{name: "embedded root", path: "/static/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			res := httptest.NewRecorder()
			assets.Handler(testLog).ServeHTTP(res, req)

			if res.Code != http.StatusNotFound {
				t.Errorf("status = %d for %q; want 404", res.Code, tt.path)
			}
		})
	}
}

// TestHasCataloguePhotographs checks that the catalogue's photographs are
// embedded.
func TestHasCataloguePhotographs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"media/products/department-books-stationery-400.webp",
		"media/products/department-tech.webp",
		"media/products/campaign-autumn-400.webp",
		"media/products/campaign-tea-week.webp",
	} {
		if !assets.Has(name) {
			t.Errorf("assets.Has(%q) = false; want the photograph embedded", name)
		}
	}
}

// TestAliasFavicon checks that the favicon is served at its fixed name.
func TestAliasFavicon(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/favicon.ico", http.NoBody)
	res := httptest.NewRecorder()
	assets.Alias(testLog, assets.FaviconICO).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("Content-Type = %q; want image/x-icon", got)
	}
	// An ICO directory opens with reserved 0, type 1.
	if !bytes.HasPrefix(res.Body.Bytes(), []byte{0, 0, 1, 0}) {
		t.Error("the body is not an icon file")
	}
}

// TestFaviconSVGDarkSchemeRule checks that the tab icon shows its ink ring on a
// dark tab bar, through a dark-scheme rule.
func TestFaviconSVGDarkSchemeRule(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(assets.FaviconSVG), http.NoBody)
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)

	body := res.Body.String()
	if res.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(zr)
		body = string(raw)
	}
	if !strings.Contains(body, "prefers-color-scheme: dark") {
		t.Errorf("the mark has no dark-scheme rule:\n%s", body)
	}
}

// TestMarkSVGNoDarkSchemeRule checks that the page mark has no dark-scheme rule.
func TestMarkSVGNoDarkSchemeRule(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, assets.URL(assets.MarkSVG), http.NoBody)
	res := httptest.NewRecorder()
	assets.Handler(testLog).ServeHTTP(res, req)

	// The operating system's scheme, not the page's, would turn the ring
	// near-white on a light page.
	if strings.Contains(res.Body.String(), "prefers-color-scheme") {
		t.Error("the page-level mark follows the operating system's colour scheme")
	}
}
