package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestInvoiceCancellationNoticesExplainTheNextAction(t *testing.T) {
	t.Parallel()
	for query, key := range map[string]i18n.Key{
		"cancelinvoice":    i18n.KeyAdminNoticeCancelInvoice,
		"invoicecancelled": i18n.KeyAdminNoticeInvoiceCancelled,
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			ctx := i18n.WithLocale(t.Context(), locale)
			req := httptest.NewRequestWithContext(ctx, "GET", "/admin/orders/GO-991230-000103?"+query+"=1", nil)
			if got := noticeFor(req); got != i18n.T(ctx, key) {
				t.Errorf("%s %s notice=%q", query, locale, got)
			}
		}
	}
}
