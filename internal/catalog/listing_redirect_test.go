package catalog

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestRedirectPastLastPage holds that the redirect past the last page keeps the
// other parameters.
func TestRedirectPastLastPage(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/c/phones", "/search"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"?page=9&q=pixelight&brand=aurora&opt=colour%3Awhite&sort=price_asc", http.NoBody)
		w := httptest.NewRecorder()
		if !redirectPastLastPage(w, r, 9, 2) || w.Code != http.StatusSeeOther {
			t.Fatalf("past last page status = %d, want 303", w.Code)
		}
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		want := r.URL.Query()
		want.Set("page", "2")
		if location.Path != path || location.RawQuery != want.Encode() {
			t.Errorf("redirect = %q, want path %q and query %q", location, path, want.Encode())
		}
		if r.URL.Query().Get("page") != "9" {
			t.Error("redirect changed the request URL")
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=match&page=2", http.NoBody)
	if redirectPastLastPage(httptest.NewRecorder(), r, 2, 2) {
		t.Error("last page redirected")
	}
	r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=match&page=501", http.NoBody)
	w := httptest.NewRecorder()
	if !redirectPastLastPage(w, r, maxPage, maxPage) || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/search?page=500&q=match" {
		t.Error("a capped page number beyond the last was displayed under the wrong address")
	}
}

// TestRedirectUnknownFilters holds that the unknown-filter redirect does not
// discard known choices whose count is zero.
func TestRedirectUnknownFilters(t *testing.T) {
	t.Parallel()
	facets := []FacetGroup{
		{Kind: FacetBrand, Options: []FacetOption{{Value: "aurora", Count: 0}}},
		{Kind: FacetVariantOption, Name: "colour", Options: []FacetOption{{Value: "colour:white", Count: 0}}},
	}
	for _, tc := range []struct {
		name, query, want string
		redirect          bool
	}{
		{"unknown brand", "brand=missing&min_price=100&page=9", "min_price=100&page=9", true},
		{"unknown option", "opt=colour%3Amissing&sort=rating", "sort=rating", true},
		{"known choices retained", "brand=aurora&brand=missing&opt=colour%3Awhite&opt=colour%3Amissing&in_stock=1", "brand=aurora&in_stock=1&opt=colour%3Awhite", true},
		{"real values match nothing", "brand=aurora&opt=colour%3Awhite&max_price=1", "", false},
		{"no filters", "page=9", "", false},
		{"malformed option", "opt=missing", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/phones?"+tc.query, http.NoBody)
			w := httptest.NewRecorder()
			if got := redirectUnknownFilters(w, r, facets); got != tc.redirect {
				t.Fatalf("redirect = %v, want %v", got, tc.redirect)
			}
			if !tc.redirect {
				return
			}
			target := "/c/phones"
			if tc.want != "" {
				target += "?" + tc.want
			}
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != target {
				t.Errorf("status/location = %d/%q, want 303/%q", w.Code, w.Header().Get("Location"), target)
			}
		})
	}
}
