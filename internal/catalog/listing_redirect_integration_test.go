//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestListingAndSearchRedirectPastLastPage(t *testing.T) {
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	listing, err := store.Listing(ctx, "phones", catalog.Filters{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	search, err := store.Search(ctx, catalog.SearchPattern("pixelight"), catalog.SortRelevance, 1)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Total == 0 || search.Total == 0 {
		t.Fatal("redirect fixture must have matches")
	}
	for _, tc := range []struct {
		target string
		last   int
	}{
		{"/c/phones?page=500&sort=rating&keep=listing", listing.Pages()},
		{"/search?q=pixelight&page=500&sort=rating&keep=search", search.Pages()},
	} {
		t.Run(tc.target, func(t *testing.T) {
			w := catalogueRequest(t, store, tc.target)
			want, err := url.Parse(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			q := want.Query()
			q.Set("page", strconv.Itoa(tc.last))
			want.RawQuery = q.Encode()
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != want.RequestURI() {
				t.Fatalf("status/location = %d/%q, want 303/%q", w.Code, w.Header().Get("Location"), want.RequestURI())
			}
			landed := catalogueRequest(t, store, w.Header().Get("Location"))
			if landed.Code != http.StatusOK {
				t.Fatalf("last page status = %d, want 200", landed.Code)
			}
		})
	}
}

func TestListingDropsUnknownFiltersButRetainsRealEmptyCombinations(t *testing.T) {
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	view, err := store.Listing(t.Context(), "phones", catalog.Filters{Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	var brand, option string
	for _, group := range view.Facets {
		for _, choice := range group.Options {
			if group.Kind == pages.FacetBrand && brand == "" {
				brand = choice.Value
			}
			if group.Kind == pages.FacetVariantOption && option == "" {
				option = choice.Value
			}
		}
	}
	if brand == "" || option == "" {
		t.Fatal("phone fixture must contain a brand and an option")
	}
	for _, tc := range []struct{ query, want string }{
		{"brand=no-such-catalogue-brand&sort=rating", "sort=rating"},
		{"opt=no-such-axis%3Ano-such-value&min_price=10", "min_price=10"},
	} {
		w := catalogueRequest(t, store, "/c/phones?"+tc.query)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/c/phones?"+tc.want {
			t.Errorf("unknown filter status/location = %d/%q", w.Code, w.Header().Get("Location"))
		}
	}
	q := url.Values{"brand": {brand}, "opt": {option}, "max_price": {"1"}}
	target := "/c/phones?" + q.Encode()
	w := catalogueRequest(t, store, target)
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyListingEmpty)) {
		t.Errorf("real zero-match filters status/location = %d/%q", w.Code, w.Header().Get("Location"))
	}
}

func catalogueRequest(t *testing.T, store *catalog.Store, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.En), http.MethodGet, target, http.NoBody)
	w := httptest.NewRecorder()
	handler := catalog.NewHandler(store, slog.New(slog.DiscardHandler), "https://goen.example")
	if strings.HasPrefix(r.URL.Path, "/c/") {
		r.SetPathValue("slug", strings.TrimPrefix(r.URL.Path, "/c/"))
		handler.Listing(w, r)
	} else {
		handler.Search(w, r)
	}
	return w
}
