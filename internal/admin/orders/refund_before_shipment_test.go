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
			if got := web.Notice(req, notices).Text; got == "" || got != i18n.T(ctx, key) {
				t.Errorf("%s %s notice=%q", query, locale, got)
			}
		}
	}
}

func TestRefundRecoveryNoticesDistinguishSettledMoney(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		query  string
		want   string
	}{
		{"settled Chinese", i18n.ZhHant, "cancelretry", "退款已完成，但訂單還沒取消。請按「繼續退款」完成取消。"},
		{"settled English", i18n.En, "cancelretry", "The refund went through, but the order is not cancelled yet. Press “Resume the refund” to finish."},
		{"unpaid Chinese", i18n.ZhHant, "refundretry", "退款沒有完成。請到 Stripe 後台確認這筆款項，再按「繼續退款」。"},
		{"unpaid English", i18n.En, "refundretry", "The refund did not complete. Check the payment in the Stripe dashboard, then press “Resume the refund”."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			req := httptest.NewRequestWithContext(ctx, "GET", "/admin/orders/GO-260929-000102?"+tt.query+"=1", nil)
			if got := web.Notice(req, notices).Text; got != tt.want {
				t.Errorf("%s notice = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
