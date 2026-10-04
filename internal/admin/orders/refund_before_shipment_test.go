package orders

import (
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestRefundBeforeShipmentNoticesExplainTheNextAction(t *testing.T) {
	t.Parallel()
	for query, key := range map[string]i18n.Key{
		"paidcancel":    i18n.KeyAdminNoticePaidCancel,
		"refunded":      i18n.KeyAdminNoticeRefunded,
		"refundpending": i18n.KeyAdminNoticeRefundPending,
		"cancelinvoice": i18n.KeyAdminNoticeCancelInvoice,
		"refundretry":   i18n.KeyAdminNoticeRefundRetry,
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			ctx := i18n.WithLocale(t.Context(), locale)
			req := httptest.NewRequestWithContext(ctx, "GET", "/admin/orders/GO-260929-000102?"+query+"=1", nil)
			if got := web.Notice(req, notices); got == "" || got != i18n.T(ctx, key) {
				t.Errorf("%s %s notice=%q", query, locale, got)
			}
		}
	}
}
