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

// TestRequiredAssetsAreVersioned covers every asset the templates name. A
// missing file already stops the binary during package initialization; this
// proves the URL each constant produces is the versioned one that the year-long
// cache header depends on.
func TestRequiredAssetsAreVersioned(t *testing.T) {
	t.Parallel()

	names := []string{
		assets.DesignSystemCSS,
		assets.AccentsCSS,
		assets.CommerceCSS,
		assets.AppCSS,
		assets.HTMXJS,
		assets.AppJS,
		assets.MarkSVG,
		assets.HomeHeroImage,
		assets.HomeHeroImage720,
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

func TestURLOfUnknownAssetIsNotVersioned(t *testing.T) {
	t.Parallel()

	got := assets.URL("css/does-not-exist.css")
	if strings.Contains(got, "?v=") {
		t.Errorf("URL() = %q; an unknown asset must not claim a version", got)
	}
}

func TestProductImageURLMapsStorageKey(t *testing.T) {
	t.Parallel()

	const key = "koto-pad-mini-01.webp"
	got := assets.ProductImageURL(key)
	wantPrefix := "/static/media/products/" + key + "?v="
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("assets.ProductImageURL(%q) = %q; want prefix %q", key, got, wantPrefix)
	}
	if digest := strings.TrimPrefix(got, wantPrefix); len(digest) != 12 {
		t.Errorf("assets.ProductImageURL(%q) digest = %q; want 12 characters", key, digest)
	}
}

func TestProductImageSrcsetMapsResponsiveVariants(t *testing.T) {
	t.Parallel()

	const key = "koto-pad-mini-01.webp"
	got := assets.ProductImageSrcset(key)
	for _, want := range []string{
		"/static/media/products/koto-pad-mini-01-400.webp?",
		" 400w, ",
		"/static/media/products/koto-pad-mini-01-800.webp?",
		" 800w, ",
		"/static/media/products/koto-pad-mini-01.webp?",
		" 1600w",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("assets.ProductImageSrcset(%q) = %q; missing %q", key, got, want)
		}
	}
}

func TestProductImageURLRejectsPathLikeStorageKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"",
		".",
		"..",
		"../hero/home-hero-01.webp",
		"nested/product.webp",
		`\windows\product.webp`,
		"product.webp?download=1",
		"product.webp#fragment",
		"not-embedded.webp",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			if got := assets.ProductImageURL(key); got != "" {
				t.Errorf("assets.ProductImageURL(%q) = %q; want empty URL", key, got)
			}
			if got := assets.ProductImageSrcset(key); got != "" {
				t.Errorf("assets.ProductImageSrcset(%q) = %q; want empty srcset", key, got)
			}
		})
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

// TestAWriteFailureUsesTheConfiguredRequestLogger catches both regressions in
// the failure-only path: falling back to slog.Default and dropping the request
// context before the record reaches its handler.
func TestAWriteFailureUsesTheConfiguredRequestLogger(t *testing.T) {
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
	if logHandler.record.Message != "assets: write gzip body" {
		t.Errorf("write warning message = %q, want %q",
			logHandler.record.Message, "assets: write gzip body")
	}
}

func TestAnAssetIsServedPrecompressed(t *testing.T) {
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

func TestASmallTextAssetHasOnlyItsIdentityRepresentation(t *testing.T) {
	t.Parallel()

	res := requestAsset(t, assets.DesignSystemCSS, "gzip", "")
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

func TestAnAssetWithoutAcceptEncodingIsIdentity(t *testing.T) {
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

func TestAssetGzipNegotiationHonoursQualityAndWildcard(t *testing.T) {
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

func TestAnImageIsNotPrecompressed(t *testing.T) {
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

func TestAnAssetETagDistinguishesEncodings(t *testing.T) {
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

func TestAnAssetMatchesAValidatorOnALaterHeaderLine(t *testing.T) {
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

func TestAHeadRequestCarriesTheSelectedRepresentationHeaders(t *testing.T) {
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

func TestHandlerRefusesLongCacheWithoutMatchingVersion(t *testing.T) {
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

func TestHandlerRefusesUnknownAndDirectories(t *testing.T) {
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

// TestAnUploadedKeyResolvesToTheMediaHandler proves a digest reaches the
// media handler and nothing else does.
//
// internal/media stores an image under the sha256 of its bytes and serves it at
// /media/{digest}; assets resolves a storage key to a URL. The two are held
// together by the digest's SHAPE, spelled out independently in both packages
// because assets must not import a feature package.
//
// This is what stops that agreement drifting: change the shape in one place and
// an uploaded product photo silently renders as the placeholder.
func TestAnUploadedKeyResolvesToTheMediaHandler(t *testing.T) {
	const digest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	if got := assets.ProductImageURL(digest); got != "/media/"+digest {
		t.Errorf("assets.ProductImageURL(digest) = %q, want /media/%s", got, digest)
	}
	// One rendition, so no srcset — rather than three URLs serving one image.
	if got := assets.ProductImageSrcset(digest); got != "" {
		t.Errorf("assets.ProductImageSrcset(digest) = %q, want empty", got)
	}

	// And nothing that is not a digest takes that path.
	for _, key := range []string{
		"aurora-edge-7-01.webp",
		"../../etc/passwd",
		strings.ToUpper(digest), // uppercase hex is not the shape
		digest[:63],             // too short
		digest + "a",            // too long
		"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a0g", // not hex
	} {
		if got := assets.ProductImageURL(key); strings.HasPrefix(got, "/media/") {
			t.Errorf("assets.ProductImageURL(%q) = %q, which reached the media handler", key, got)
		}
	}
}
