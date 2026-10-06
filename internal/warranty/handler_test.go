package warranty

import (
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestWarrantyRedirectNoticesAreSuccessOnly(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name    string
			query   string
			success bool
		}{
			{name: "no query"},
			{name: "stale serial", query: "?serial=1"},
			{name: "stale refused", query: "?refused=1"},
			{name: "both stale", query: "?serial=1&refused=1"},
			{name: "success", query: "?ok=1", success: true},
			{name: "success with stale queries", query: "?ok=1&serial=1&refused=1", success: true},
		} {
			t.Run(locale.Tag()+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				r := httptest.NewRequestWithContext(ctx, "GET", "/account/warranty/order"+tc.query, nil)
				want := ""
				if tc.success {
					want = i18n.T(ctx, i18n.KeyWarrantyAlready)
				}
				if got := noticeFor(r); got != want {
					t.Errorf("notice = %q, want %q", got, want)
				}
			})
		}
	}
}
