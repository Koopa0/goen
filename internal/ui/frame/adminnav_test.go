package frame

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestWithHealthTaskCount: background navigation distinguishes known counts from failed reads.
func TestWithHealthTaskCount(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name   string
			count  int64
			known  bool
			zh, en string
		}{
			{"none", 0, true, "0 件要處理", "0 tasks need attention"},
			{"one", 1, true, "1 件要處理", "1 task needs attention"},
			{"many", 112, true, "112 件要處理", "112 tasks need attention"},
			{"unknown", 0, false, "待辦數無法查詢", "Task count unavailable"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := WithHealthTaskCount(i18n.WithLocale(t.Context(), locale), tt.count, tt.known)
				var body bytes.Buffer
				if err := Admin(Page{}, "products").Render(ctx, &body); err != nil {
					t.Fatal(err)
				}
				link := regexp.MustCompile(`<a[^>]*href="/admin/health"[^>]*>(.*?)</a>`).FindStringSubmatch(body.String())
				if len(link) != 2 {
					t.Fatal("background navigation link is missing")
				}
				want := tt.en
				if locale == i18n.ZhHant {
					want = tt.zh
				}
				if !strings.Contains(link[1], want) {
					t.Errorf("background link=%q, want %q", link[1], want)
				}
			})
		}
	}
}

// TestAdminLeavesToShopOnce: the way back to the shop is the bar's link; the
// side navigation does not repeat it.
func TestAdminLeavesToShopOnce(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	var body bytes.Buffer
	if err := Admin(Page{}, "products").Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(body.String(), i18n.T(ctx, i18n.KeyBackToShop)); got != 1 {
		t.Errorf("%q appears %d times on a back-office page, want 1", i18n.T(ctx, i18n.KeyBackToShop), got)
	}
}
