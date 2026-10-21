package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An untouched filter form submits min_price=&max_price=&sort=; none of it
// belongs in an address a shopper shares.
func TestAListingFormSubmittedUntouchedLandsOnACleanAddress(t *testing.T) {
	t.Parallel()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/audio?brand=aurora&min_price=&max_price=&sort=", http.NoBody)
	res := httptest.NewRecorder()
	h.Listing(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/c/audio?brand=aurora" {
		t.Errorf("status %d to %q, want 303 to /c/audio?brand=aurora", res.Code, res.Header().Get("Location"))
	}
}

// A search box submitted blank is still a search: its q stays in the address
// and the filters beside it, which are empty, go.
func TestASearchFormSubmittedBlankKeepsOnlyItsQuery(t *testing.T) {
	t.Parallel()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?q=&min_price=&sort=", http.NoBody)
	res := httptest.NewRecorder()
	h.Search(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/search?q=" {
		t.Errorf("status %d to %q, want 303 to /search?q=", res.Code, res.Header().Get("Location"))
	}
}
