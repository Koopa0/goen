package stock

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postedFrom(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/stock/adjust", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return req
}

// TestStockBackFilterAndPage: a stock write returns to the filter and page it
// was made on.
func TestStockBackFilterAndPage(t *testing.T) {
	t.Parallel()
	got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {"/admin/stock?soldout=1&after=TOKEN"}}), "price")
	if want := "/admin/stock?after=TOKEN&in=A-1&price=1&soldout=1#row-A-1"; got != want {
		t.Errorf("stockBack = %q, want %q", got, want)
	}
}

// TestStockBackRefusesForeign: a stock write cannot be sent somewhere else.
func TestStockBackRefusesForeign(t *testing.T) {
	t.Parallel()
	for _, back := range []string{"https://evil.example/x", "//evil.example/admin/stock", "/admin/orders?soldout=1", "javascript:alert(1)", ""} {
		got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {back}}), "refused")
		if want := "/admin/stock?in=A-1&refused=1#row-A-1"; got != want {
			t.Errorf("return %q gave %q, want %q", back, got, want)
		}
	}
}

// TestStockBackSearch: a stock write returns to the search it was made under.
func TestStockBackSearch(t *testing.T) {
	t.Parallel()
	got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {"/admin/stock?q=koto+cbl&after=TOKEN"}}), "arrival")
	if want := "/admin/stock?after=TOKEN&arrival=1&in=A-1&q=koto+cbl#row-A-1"; got != want {
		t.Errorf("stockBack = %q, want %q", got, want)
	}
}

// TestStockBackNamesTheRow: a write returns with the row it was made on in the
// query, because the fragment alone never reaches the server; a SKU that needs
// escaping comes back as it was posted.
func TestStockBackNamesTheRow(t *testing.T) {
	t.Parallel()
	got := stockBack(postedFrom(t, url.Values{"sku": {"A 1&B/2"}, "return": {"/admin/stock"}}), "price")
	location, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if in := location.Query().Get("in"); in != "A 1&B/2" {
		t.Errorf("stockBack(%q) names the row %q, want %q", got, in, "A 1&B/2")
	}
	if location.Query().Get("price") != "1" {
		t.Errorf("stockBack(%q) lost its notice", got)
	}
}

// TestStockReturnOfTheList: the list's own address, which its forms post back,
// names no row and no notice, so a write made after a notice does not repeat it.
func TestStockReturnOfTheList(t *testing.T) {
	t.Parallel()
	if got, want := stockReturn("1", "koto", "TOKEN", "", ""), "/admin/stock?after=TOKEN&q=koto&soldout=1"; got != want {
		t.Errorf("stockReturn = %q, want %q", got, want)
	}
}
