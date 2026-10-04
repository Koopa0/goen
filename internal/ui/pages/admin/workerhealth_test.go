package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCancelledRefundHealthStatusIsLocalized(t *testing.T) {
	t.Parallel()
	refund := OpenRefund{Status: "cancelled"}
	tests := []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "金流端取消了這筆退款，錢沒有退出去，請從退貨清單重新退款"},
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

// TestAPaidOrderWithNoInvoiceOperationIsWork: the page reads unhealthy, says
// how many there are rather than how many it lists, and puts the order and its
// issue action in front of the reader.
func TestAPaidOrderWithNoInvoiceOperationIsWork(t *testing.T) {
	t.Parallel()
	view := &WorkerHealthView{CopurchaseEverBuilt: true, CopurchaseStaleAfter: time.Hour}
	if !view.AllHealthy() {
		t.Fatal("the fixture is unhealthy before any order is listed; the check below would prove nothing")
	}
	view.UninvoicedCount = 51
	view.Uninvoiced = []UninvoicedOrder{{OrderNumber: "GO-261002-000001", AmountCents: 129900}}
	if view.AllHealthy() {
		t.Error("the page reads healthy with a paid order that has no invoice operation")
	}
	html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
	for _, want := range []string{
		`href="/admin/orders/GO-261002-000001"`,
		`action="/admin/orders/GO-261002-000001/invoice"`,
		i18n.Count(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminHPUninvoicedHint, 51, int64(51)),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the health page does not carry %s", want)
		}
	}
}

// TestALiveInvoiceOnACancelledOrderIsWork: a cancellation that could not void
// its invoice leaves staff a correction to make, so the page reads unhealthy,
// counts them all and links each order.
func TestALiveInvoiceOnACancelledOrderIsWork(t *testing.T) {
	t.Parallel()
	view := &WorkerHealthView{CopurchaseEverBuilt: true, CopurchaseStaleAfter: time.Hour}
	if !view.AllHealthy() {
		t.Fatal("the fixture is unhealthy before any invoice is listed; the check below would prove nothing")
	}
	view.CancelledOrderInvoiceCount = 3
	view.CancelledOrderInvoices = []CancelledOrderInvoice{{
		OrderNumber: "GO-261002-000002", Number: "AB12345678", AmountCents: 106000, IssuedOn: "2026-06-30",
	}}
	if view.AllHealthy() {
		t.Error("the page reads healthy with a live invoice on a cancelled order")
	}
	html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
	for _, want := range []string{
		`href="/admin/orders/GO-261002-000002"`,
		"AB12345678",
		i18n.Count(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminHPCancelledOrderInvoicesHint, 3, int64(3)),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the health page does not carry %s", want)
		}
	}
}

// TestALapsedAllowanceResendAsksTheBuyerAgain: the resend of an allowance the
// customer never agreed to is not worded as one ECPay never received.
func TestALapsedAllowanceResendAsksTheBuyerAgain(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name      string
		lastError string
		want      i18n.Key
		not       i18n.Key
	}{
		{name: "lapsed", lastError: "allowance_buyer_unconfirmed",
			want: i18n.KeyAdminHPAllowanceLapsedConfirm, not: i18n.KeyAdminHPAllowanceAbsentConfirm},
		{name: "never seen", lastError: "allowance_not_yet_visible",
			want: i18n.KeyAdminHPAllowanceAbsentConfirm, not: i18n.KeyAdminHPAllowanceLapsedConfirm},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := &WorkerHealthView{StrandedClaims: []StrandedClaim{{
				Operation: "0199aaaa-0000-7000-8000-000000000001", OrderNumber: "GO-261004-000001",
				Kind: "allowance", Status: "attention", LastError: tt.lastError, CanAuthorizeResend: true,
			}}}
			html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
			if !strings.Contains(html, i18n.T(ctx, tt.want)) || strings.Contains(html, i18n.T(ctx, tt.not)) {
				t.Errorf("%s resend is not worded as %q", tt.lastError, i18n.T(ctx, tt.want))
			}
		})
	}
}
