package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestHandler_ListingCleanAddress holds that a listing form submitted untouched
// lands on a clean address.
//
// An untouched filter form submits price=&c=&sort=; none of it
// belongs in an address a shopper shares.
func TestHandler_ListingCleanAddress(t *testing.T) {
	t.Parallel()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/audio?brand=aurora&price=&sort=", http.NoBody)
	res := httptest.NewRecorder()
	h.Listing(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/c/audio?brand=aurora" {
		t.Errorf("status %d to %q, want 303 to /c/audio?brand=aurora", res.Code, res.Header().Get("Location"))
	}
}

// TestParseFiltersBands holds that a price band, the sale switch and the stock
// switch are read from their own words only, and that the address written back
// names them the same way: an unknown band or a switch set to anything but 1
// filters nothing and is not written.
func TestParseFiltersBands(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		query url.Values
		want  Filters
		back  string
	}{
		{"band and switches", url.Values{"price": {"1000-9999"}, "sale": {"1"}, "in_stock": {"1"}},
			Filters{Price: PriceBand{ID: "1000-9999", Min: 100_000, Max: 999_999}, OnSaleOnly: true, InStockOnly: true, Sort: SortNewest, Page: 1},
			"in_stock=1&price=1000-9999&sale=1"},
		{"the cheapest band", url.Values{"price": {"under-1000"}},
			Filters{Price: PriceBand{ID: "under-1000", Max: 99_999}, Sort: SortNewest, Page: 1}, "price=under-1000"},
		{"unknown band and switch words", url.Values{"price": {"0-1"}, "sale": {"yes"}, "in_stock": {"true"}},
			Filters{Sort: SortNewest, Page: 1}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseFilters(tt.query, SortNewest)
			if got.Price != tt.want.Price || got.OnSaleOnly != tt.want.OnSaleOnly || got.InStockOnly != tt.want.InStockOnly || got.Active() != tt.want.Active() {
				t.Errorf("parseFilters(%v) = %+v, want %+v", tt.query, got, tt.want)
			}
			if back := got.Query().Encode(); back != tt.back {
				t.Errorf("parseFilters(%v).Query() = %q, want %q", tt.query, back, tt.back)
			}
		})
	}
}

// TestHandler_SearchBlankQuery holds that a search form submitted blank keeps
// only its query.
//
// A search box submitted blank is still a search: its q stays in the address
// and the filters beside it, which are empty, go.
func TestHandler_SearchBlankQuery(t *testing.T) {
	t.Parallel()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=&c=&price=&sort=", http.NoBody)
	res := httptest.NewRecorder()
	h.Search(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/search?q=" {
		t.Errorf("status %d to %q, want 303 to /search?q=", res.Code, res.Header().Get("Location"))
	}
}
