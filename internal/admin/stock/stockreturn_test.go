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

func TestAStockWriteReturnsToTheFilterAndPageItWasMadeOn(t *testing.T) {
	t.Parallel()
	got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {"/admin/stock?soldout=1&after=TOKEN"}}), "ok")
	if want := "/admin/stock?after=TOKEN&ok=1&soldout=1#row-A-1"; got != want {
		t.Errorf("stockBack = %q, want %q", got, want)
	}
}

func TestAStockWriteCannotBeSentSomewhereElse(t *testing.T) {
	t.Parallel()
	for _, back := range []string{"https://evil.example/x", "//evil.example/admin/stock", "/admin/orders?soldout=1", "javascript:alert(1)", ""} {
		got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {back}}), "refused")
		if want := "/admin/stock?refused=1#row-A-1"; got != want {
			t.Errorf("return %q gave %q, want %q", back, got, want)
		}
	}
}

func TestAStockWriteReturnsToTheSearchItWasMadeUnder(t *testing.T) {
	t.Parallel()
	got := stockBack(postedFrom(t, url.Values{"sku": {"A-1"}, "return": {"/admin/stock?q=koto+cbl&after=TOKEN"}}), "ok")
	if want := "/admin/stock?after=TOKEN&ok=1&q=koto+cbl#row-A-1"; got != want {
		t.Errorf("stockBack = %q, want %q", got, want)
	}
}
