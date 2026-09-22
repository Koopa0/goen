//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/comparison"
	"github.com/koopa0/goen/internal/i18n"
)

func comparisonPost(t *testing.T, handler http.HandlerFunc, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/compare", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("comparison POST = %d: %s", w.Code, w.Body.String())
	}
	return w
}

func comparisonCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == comparison.CookieName {
			return c
		}
	}
	t.Fatal("selection action did not write a comparison cookie")
	return nil
}

func TestComparisonSelectionPOSTsAndReadOnlySnapshots(t *testing.T) {
	h := catalog.NewHandler(catalog.NewStore(pool), slog.New(slog.DiscardHandler), false)
	slugs := activeSlugs(t, 5)
	var cookie *http.Cookie
	for _, slug := range slugs[:4] {
		w := comparisonPost(t, h.AddComparison, url.Values{"slug": {slug}, "next": {"/p/" + slug + "?color=black"}}, cookie)
		cookie = comparisonCookie(t, w)
		if !strings.Contains(w.Header().Get("Location"), "color=black&compare=added#buybox") {
			t.Fatalf("PDP options/anchor lost: %s", w.Header().Get("Location"))
		}
	}
	if cookie.Value != strings.Join(slugs[:4], ",") {
		t.Fatalf("personal selection = %q", cookie.Value)
	}
	full := comparisonPost(t, h.AddComparison, url.Values{"slug": {slugs[4]}}, cookie)
	if full.Header().Get("Location") != "/compare?compare=full" || full.Header().Get("Set-Cookie") != "" {
		t.Fatal("fifth product silently changed the selection")
	}
	duplicate := comparisonPost(t, h.AddComparison, url.Values{"slug": {slugs[0]}}, cookie)
	if got := comparisonCookie(t, duplicate).Value; got != cookie.Value {
		t.Fatalf("duplicate selection = %q", got)
	}

	for _, target := range []string{comparison.Href([]string{slugs[4]}), "/compare?p="} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.Compare(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") != "" || !strings.Contains(w.Body.String(), i18n.T(t.Context(), i18n.KeyCompareSnapshot)) {
			t.Fatalf("shared GET changed personal state: status=%d cookie=%s", w.Code, w.Header().Get("Set-Cookie"))
		}
	}
	removed := comparisonPost(t, h.RemoveComparison, url.Values{"slug": {slugs[0]}}, cookie)
	cookie = comparisonCookie(t, removed)
	if cookie.Value != strings.Join(slugs[1:4], ",") {
		t.Fatalf("remove = %q", cookie.Value)
	}
	saved := comparisonPost(t, h.SaveComparison, url.Values{"snapshot": {"1"}, "p": {slugs[4]}}, cookie)
	cookie = comparisonCookie(t, saved)
	if cookie.Value != slugs[4] {
		t.Fatalf("explicit save = %q", cookie.Value)
	}
	cleared := comparisonPost(t, h.ClearComparison, nil, cookie)
	if c := comparisonCookie(t, cleared); c.MaxAge != -1 || c.Value != "" {
		t.Fatalf("clear = %+v", c)
	}
}

func TestComparisonBuilderSearchesAndRefusesUnavailableProducts(t *testing.T) {
	h := catalog.NewHandler(catalog.NewStore(pool), slog.New(slog.DiscardHandler), false)
	slug := activeSlugs(t, 1)[0]
	var name string
	if err := pool.QueryRow(t.Context(), `SELECT name FROM products WHERE slug = $1`, slug).Scan(&name); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/compare?q="+url.QueryEscape(name), http.NoBody)
	w := httptest.NewRecorder()
	h.Compare(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/compare/add"`) || !strings.Contains(w.Body.String(), `value="`+slug+`"`) {
		t.Fatalf("builder did not expose matching product: status %d", w.Code)
	}
	unavailable := comparisonPost(t, h.AddComparison, url.Values{"slug": {"not-a-current-product"}}, nil)
	if unavailable.Header().Get("Location") != "/compare?compare=unavailable" || unavailable.Header().Get("Set-Cookie") != "" {
		t.Fatal("unknown product was persisted")
	}
}
