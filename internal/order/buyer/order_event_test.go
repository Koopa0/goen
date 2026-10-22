package buyer

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestOrderEvent_LabelKey: LabelKey gives each of the nine event kinds its
// Traditional Chinese history label.
func TestOrderEvent_LabelKey(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for kind, want := range map[order.EventKind]string{
		order.EventPlaced:    "送出訂單",
		order.EventPaid:      "付款完成",
		order.EventPicking:   "開始備貨",
		order.EventShipped:   "出貨",
		order.EventInTransit: "運送中",
		order.EventDelivered: "送達",
		order.EventCompleted: "訂單完成",
		order.EventCancelled: "取消",
		order.EventRefunded:  "已退款",
	} {
		if got := i18n.T(ctx, OrderEvent{Kind: kind}.LabelKey()); got != want {
			t.Errorf("LabelKey(%s) reads %q, want %q", kind, got, want)
		}
	}
}

// TestOrderEvent_LabelKeyUnsent: the refund event of units the shop could not
// send takes the not-sent-and-refunded label in both locales, and a return's
// refund does not.
func TestOrderEvent_LabelKeyUnsent(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "無法出貨，已退款", i18n.En: "Not sent — refunded"} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded, Unsent: true}.LabelKey()); got != want {
			t.Errorf("%s: the refund of unsent units reads %q, want %q", locale, got, want)
		}
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded}.LabelKey()); got == want {
			t.Errorf("%s: a return's refund reads %q too", locale, got)
		}
	}
}
