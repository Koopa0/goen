package assets_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
)

// TestRequiredAssetsAreVersioned covers every asset the templates name. A
// missing file already stops the binary in init; this proves the URL each
// constant produces is the versioned one that the year-long cache header
// depends on.
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
	assets.Handler().ServeHTTP(res, req)

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
			assets.Handler().ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200", res.Code)
			}
			if got := res.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q; want %q", got, "no-cache")
			}
		})
	}
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
			assets.Handler().ServeHTTP(res, req)

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
