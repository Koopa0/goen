package orders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// The void and allowance forms redirect here with these notices, whose words
// must not send staff after the fields only the issue form collects.
func TestVoidAndAllowanceNoticesDoNotBlameTaxIDs(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, name := range []string{"voidfailed", "allowfailed", "voidreason"} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet,
			"/admin/orders/GO-260901-000001?"+name+"=1", nil)
		got := web.Notice(req, notices)
		if got == "" {
			t.Errorf("%s has no notice", name)
		}
		if strings.Contains(got, "統編") || strings.Contains(got, "載具") {
			t.Errorf("%s notice %q names Issue fields a void/折讓 form does not collect",
				name, got)
		}
	}
}

// An invoice write with no provider configured redirects with refused, so its
// words must not name a provider that was never called.
func TestTheRefusedNoticeNamesNoProvider(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.ZhHant), http.MethodGet,
		"/admin/orders/GO-260901-000001?refused=1", nil)
	got := web.Notice(req, notices)
	if got == "" {
		t.Fatal("refused has no notice")
	}
	if strings.Contains(got, "加值中心") || strings.Contains(got, "綠界") {
		t.Errorf("refused notice %q names a provider that was never called", got)
	}
}
