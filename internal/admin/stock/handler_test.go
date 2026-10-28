package stock

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestHandler_RedirectNotices: every redirect the stock forms make carries a
// message.
func TestHandler_RedirectNotices(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z-]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	// The stock list's writes name their notice through stockBack.
	for _, m := range regexp.MustCompile(`stockBack\(r, "([a-z-]+)"\)`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

// TestPostedFromStockList: only a form that names the stock list itself is
// answered on the list; the SKU page and a foreign address are not.
func TestPostedFromStockList(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, back string
		want       bool
	}{
		{"the list", "/admin/stock", true},
		{"a filtered page of the list", "/admin/stock?soldout=1&after=T", true},
		{"nothing", "", false},
		{"the SKU page", "/admin/stock/SKU-1", false},
		{"another host", "https://example.com/admin/stock", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/stock/receive", strings.NewReader(url.Values{"return": {tt.back}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := postedFromStockList(req); got != tt.want {
				t.Errorf("postedFromStockList(%q) = %t, want %t", tt.back, got, tt.want)
			}
		})
	}
}
