package stock

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
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
		_, onList := listNotices[name]
		_, onSKU := skuNotices[name]
		if !onList && !onSKU {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for _, group := range []map[string]widget.NoticeEntry{listNotices, skuNotices} {
		for name := range group {
			if !sent[name] {
				t.Errorf("notice %q names a parameter no redirect writes", name)
			}
		}
	}
}

// TestNoticesBelongToTheirPage: a code is shown by the page its write returns
// to and by no other, so ?adjusted=1 on the list and ?price=1 on a SKU page say
// nothing.
func TestNoticesBelongToTheirPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code        string
		list, onSKU bool
	}{
		{"price", true, false},
		{"arrival", true, false},
		{"adjusted", false, true},
		{"on", false, true},
		{"off", false, true},
		{"receipt-conflict", false, true},
		{"received", true, true},
		{"refused", true, true},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/stock?"+tc.code+"=1", http.NoBody)
		if shown := widget.NoticeFrom(req, listNotices).Text != ""; shown != tc.list {
			t.Errorf("?%s=1 on the stock list shows a notice = %t, want %t", tc.code, shown, tc.list)
		}
		if shown := widget.NoticeFrom(req, skuNotices).Text != ""; shown != tc.onSKU {
			t.Errorf("?%s=1 on a SKU page shows a notice = %t, want %t", tc.code, shown, tc.onSKU)
		}
	}
}

// TestDoneNoticesAreDistinct: no two actions share a sentence, so a save says
// which change it was.
func TestDoneNoticesAreDistinct(t *testing.T) {
	t.Parallel()
	for _, group := range []map[string]widget.NoticeEntry{listNotices, skuNotices} {
		seen := map[i18n.Key]string{}
		for name, e := range group {
			if e.Outcome != widget.OutcomeDone {
				continue
			}
			if first, dup := seen[e.Key]; dup {
				t.Errorf("?%s=1 and ?%s=1 both say %q", first, name, e.Key)
			}
			seen[e.Key] = name
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
