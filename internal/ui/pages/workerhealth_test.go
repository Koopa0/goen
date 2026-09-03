package pages

import (
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestCancelledRefundHealthStatusIsLocalized(t *testing.T) {
	t.Parallel()
	refund := OpenRefund{Status: "cancelled"}
	tests := []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "金流端取消了這筆退款,錢沒有退出去,請從退貨清單重新退款"},
		{name: "English", locale: i18n.En, want: "The provider cancelled it: no money moved; retry it from the returns queue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := refund.StatusText(ctx); got != tt.want {
				t.Errorf("cancelled refund status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefundHealthUsesTheExactCountNotTheBoundedSample(t *testing.T) {
	t.Parallel()
	view := WorkerHealthView{
		OpenRefundCount: 37,
		OpenRefunds:     make([]OpenRefund, 20),
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if view.RefundsHealthy() {
		t.Fatal("a non-zero exact refund count reads healthy")
	}
	want := fmt.Sprintf(i18n.T(ctx, i18n.KeyHealthRefundsStuck), int64(37))
	if got := view.RefundsText(ctx); got != want {
		t.Errorf("refund health text = %q, want exact-count text %q", got, want)
	}
}
