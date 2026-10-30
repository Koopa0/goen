//go:build integration

package stock_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/i18n"
)

// noticeRows reads a stock list and returns, for each time sentence appears, the
// id of the row whose own row it follows; "head" is outside the table.
func noticeRows(t *testing.T, body, sentence string) (places []string) {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != nethtml.TextNode || !strings.Contains(n.Data, sentence) {
			continue
		}
		place := "head"
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type != nethtml.ElementNode || p.Data != "tr" {
				continue
			}
			place = "table"
			for prev := p.PrevSibling; prev != nil; prev = prev.PrevSibling {
				if prev.Type == nethtml.ElementNode {
					for _, a := range prev.Attr {
						if a.Key == "id" {
							place = a.Val
						}
					}
					break
				}
			}
			break
		}
		places = append(places, place)
	}
	return places
}

// TestHandler_ListNoticeUnderRow: a write made on a row of the stock list returns
// to the list with the row named in the query, and the list shows the answer
// under that row and nowhere else; a row the list no longer holds leaves the
// answer at the head.
func TestHandler_ListNoticeUnderRow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	ctx = i18n.WithLocale(ctx, i18n.En)
	mux := http.NewServeMux()
	handlerOver(stock.NewStore(admintest.AdminRolePool(t, pool))).Routes(mux, admintest.BackOffice)
	sku, _ := receiptVariant(t, pool)
	other, _ := receiptVariant(t, pool)
	list := "/admin/stock?q=" + url.QueryEscape(sku)

	get := func(target string) string {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", target, w.Code)
		}
		return w.Body.String()
	}

	for _, tc := range []struct {
		name, path string
		form       url.Values
		sentence   i18n.Key
	}{
		{"price", "/admin/stock/price", url.Values{"price": {"250"}, "compare_at": {""}}, i18n.KeyAdminNoticePriceSaved},
		{"refused price", "/admin/stock/price", url.Values{"price": {"0"}, "compare_at": {""}}, i18n.KeyAdminNoticeRefused},
		{"arrival", "/admin/stock/arrival", url.Values{"arrival_on": {"2030-01-02"}}, i18n.KeyAdminNoticeArrivalSaved},
		{"receipt", "/admin/stock/receive", url.Values{"quantity": {"3"}, "idempotency": {"row-notice-" + sku}}, i18n.KeyAdminNoticeReceived},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := tc.form
			form.Set("sku", sku)
			form.Set("return", list)
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, tc.path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			location, err := url.Parse(w.Header().Get("Location"))
			if w.Code != http.StatusSeeOther || err != nil || location.Path != "/admin/stock" ||
				location.Query().Get("in") != sku || location.Fragment != "row-"+sku {
				t.Fatalf("POST %s = %d %q, want 303 to the list naming %s in the query and in the fragment", tc.path, w.Code, w.Header().Get("Location"), sku)
			}

			sentence := i18n.T(ctx, tc.sentence)
			if got, want := noticeRows(t, get(location.RequestURI()), sentence), []string{"row-" + sku}; strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("after %s the answer %q stands at %v, want %v", tc.name, sentence, got, want)
			}
		})
	}

	t.Run("row not listed", func(t *testing.T) {
		sentence := i18n.T(ctx, i18n.KeyAdminNoticePriceSaved)
		if got, want := noticeRows(t, get(list+"&price=1&in="+url.QueryEscape(other)), sentence), []string{"head"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("a notice for a row the list does not hold stands at %v, want %v", got, want)
		}
	})

	t.Run("no notice", func(t *testing.T) {
		if body := get(list + "&in=" + url.QueryEscape(sku)); strings.Contains(body, "colspan") {
			t.Error("a row named with no notice to show still gets a row of its own")
		}
	})
}
