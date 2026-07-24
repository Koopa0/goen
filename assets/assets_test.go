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
