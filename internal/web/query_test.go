package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/web"
)

func TestDropEmptyParamsRedirectsABrowserToTheAddressWithoutThem(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/audio?brand=aurora&min_price=&max_price=&sort=", http.NoBody)
	w := httptest.NewRecorder()
	if !web.DropEmptyParams(w, r) {
		t.Fatal("a query with empty parameters was not redirected")
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/c/audio?brand=aurora" {
		t.Errorf("status %d to %q, want 303 to /c/audio?brand=aurora", w.Code, w.Header().Get("Location"))
	}
}

func TestDropEmptyParamsLeavesACleanQueryAlone(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=pro&sort=rating", http.NoBody)
	w := httptest.NewRecorder()
	if web.DropEmptyParams(w, r) || w.Code != http.StatusOK || w.Header().Get("HX-Push-Url") != "" {
		t.Errorf("a clean query was touched: redirected status %d, push %q", w.Code, w.Header().Get("HX-Push-Url"))
	}
}

func TestDropEmptyParamsPushesTheCleanAddressForHTMX(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=pro&sort=", http.NoBody)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	if web.DropEmptyParams(w, r) {
		t.Fatal("an htmx request was redirected, so the swap would never happen")
	}
	if got := w.Header().Get("HX-Push-Url"); got != "/search?q=pro" {
		t.Errorf("HX-Push-Url = %q, want /search?q=pro", got)
	}
}

func TestDropEmptyParamsKeepsTheNamedOnes(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=&sort=", http.NoBody)
	w := httptest.NewRecorder()
	if !web.DropEmptyParams(w, r, "q") || w.Header().Get("Location") != "/search?q=" {
		t.Errorf("Location = %q, want /search?q=", w.Header().Get("Location"))
	}
}
