package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/refundstate"
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
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "金流端取消了這筆退款，錢沒有退出去"},
		{name: "English", locale: i18n.En, want: "The provider cancelled this refund attempt: no money moved"},
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

func TestOpenRefundRowsLinkTheOrderAndExplainManualRecovery(t *testing.T) {
	t.Parallel()
	states := []struct {
		name   string
		state  refundstate.State
		status [2]string
		next   [2]string
	}{
		{
			name:   "pending",
			state:  refundstate.Pending,
			status: [2]string{"已送出，還沒收到金流端的結果", "Sent, no answer from the provider yet"},
			next:   [2]string{"請在 Stripe 查詢退款結果。", "Check the refund status in Stripe."},
		},
		{
			name:   "requires action",
			state:  refundstate.RequiresAction,
			status: [2]string{"金流端說還需要處理才會退出去", "The provider says something more is needed before the money moves"},
			next:   [2]string{"請先查看 Stripe 顯示的退款處理指示。", "Read the refund action instructions shown in Stripe first."},
		},
		{
			name:   "failed",
			state:  refundstate.Failed,
			status: [2]string{"金流端拒絕了這筆退款，錢沒有退出去", "The provider refused this refund: no money moved"},
			next:   [2]string{"請在 Stripe 查明退款失敗原因。", "Check why the refund failed in Stripe."},
		},
		{
			name:   "cancelled attempt",
			state:  refundstate.Cancelled,
			status: [2]string{"金流端取消了這筆退款，錢沒有退出去", "The provider cancelled this refund attempt: no money moved"},
			next:   [2]string{"請在 Stripe 查明這筆退款被取消的原因。", "Check why this refund attempt was cancelled in Stripe."},
		},
	}
	origins := []struct {
		name     string
		key      string
		recovery [2]string
	}{
		{
			name: "return",
			key:  "return:0199aaaa-0000-7000-8000-000000000001",
			recovery: [2]string{
				"goen 不會自動接續這筆退款；訂單或退貨頁若提供「繼續退款」或「重新退款」，才可使用該操作核對並繼續退款。",
				"goen does not automatically resume this refund; use “Resume the refund” or “Send the refund again” on the order or returns page only if offered to check and continue it.",
			},
		},
		{
			name: "non-return",
			key:  "cancel:0199aaaa-0000-7000-8000-000000000002",
			recovery: [2]string{
				"goen 不會自動接續這筆退款；請依 Stripe 顯示的狀態與指示處理，此頁不提供重試操作。",
				"goen does not automatically resume this refund; follow the status and instructions shown in Stripe. This page offers no retry action.",
			},
		},
	}
	for _, tt := range states {
		for _, origin := range origins {
			for index, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				t.Run(tt.name+"/"+origin.name+"/"+string(locale), func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale)
					view := WorkerHealthView{OpenRefunds: []OpenRefund{{
						OrderNumber: "GO-261006-000004", Key: origin.key, Status: tt.state,
					}}}
					html := renderComponent(t, ctx, Health(layouts.Page{}, &view))
					for _, want := range []string{
						`<a href="/admin/orders/GO-261006-000004">GO-261006-000004</a>`,
						tt.status[index],
						`<p class="goen-admin__hint">` + tt.next[index] + " " + origin.recovery[index] + `</p>`,
					} {
						if !strings.Contains(html, want) {
							t.Errorf("refund row lacks %q", want)
						}
					}
				})
			}
		}
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

// TestARefundStripeFailedNamesItsOrderAndAmount: the alarm for a refund goen
// recorded as succeeded is worked from the order and the sum to repay, and only
// that event carries them and the instruction.
func TestARefundStripeFailedNamesItsOrderAndAmount(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &WorkerHealthView{UnreconciledEvents: []UnreconciledEvent{
		{
			EventID: "evt_refund_failed", Type: "refund.failed", Ref: "re_3Q1abc",
			Reason:            "refund_failed: lost_or_stolen_card",
			RefundOrderNumber: "GO-261006-000003", RefundCents: 120000,
		},
		{
			EventID: "evt_unreadable", Type: "checkout.session.completed", Ref: "cs_test_1",
			Reason: "unreadable_event: goen could not read a checkout.session.completed it acts on",
		},
	}}
	html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
	for _, want := range []string{
		`href="/admin/orders/GO-261006-000003"`,
		money.TWD(120000),
		"lost_or_stolen_card",
		`name="event" value="evt_refund_failed"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the failed refund's row does not carry %s", want)
		}
	}
	if got := strings.Count(html, `href="/admin/orders/`); got != 1 {
		t.Errorf("the page links %d orders, want only the failed refund's", got)
	}
	if got := strings.Count(html, i18n.T(ctx, i18n.KeyAdminHPRefundFailedAtStripe)); got != 1 {
		t.Errorf("the repay instruction appears %d times, want once, on the failed refund", got)
	}
}

func TestDisputesRowRendersWhatStripeSaid(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	healthy := func() *WorkerHealthView {
		return &WorkerHealthView{CopurchaseEverBuilt: true, CopurchaseStaleAfter: time.Hour}
	}
	tests := []struct {
		name    string
		state   DisputeState
		healthy bool
		want    []string
	}{
		{
			name: "unknown", state: DisputeState{Configured: true, Unknown: true},
			want: []string{i18n.T(ctx, i18n.KeyAdminHPDisputesName), i18n.T(ctx, i18n.KeyHealthDisputesUnknown), i18n.T(ctx, i18n.KeyAdminHPNeedsLook)},
		},
		{
			name: "no order and no deadline",
			state: DisputeState{Configured: true, Items: []OpenDispute{
				{URL: "https://dashboard.stripe.com/disputes/dp_1", AmountCents: 700, Currency: "twd"},
			}},
			want: []string{
				i18n.T(ctx, i18n.KeyAdminHPDisputeNoOrder), i18n.T(ctx, i18n.KeyAdminHPDisputeNoDeadline),
				`href="https://dashboard.stripe.com/disputes/dp_1"`, i18n.T(ctx, i18n.KeyAdminHPNeedsLook),
			},
		},
		{
			name: "orders could not be looked up",
			state: DisputeState{Configured: true, OrdersUnknown: true, Items: []OpenDispute{
				{URL: "https://dashboard.stripe.com/disputes/dp_1", AmountCents: 700, Currency: "twd"},
			}},
			want: []string{i18n.T(ctx, i18n.KeyAdminHPDisputeOrderUnknown), i18n.T(ctx, i18n.KeyAdminHPNeedsLook)},
		},
		{
			name:    "none",
			state:   DisputeState{Configured: true},
			healthy: true,
			want:    []string{i18n.T(ctx, i18n.KeyHealthDisputesClear)},
		},
		{name: "not configured", state: DisputeState{}, healthy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := healthy()
			view.Disputes = tt.state
			if got := view.AllHealthy(); got != tt.healthy {
				t.Errorf("AllHealthy() = %v, want %v", got, tt.healthy)
			}
			html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
			for _, want := range tt.want {
				if !strings.Contains(html, want) {
					t.Errorf("the health page does not carry %q", want)
				}
			}
			if !tt.state.Configured && strings.Contains(html, i18n.T(ctx, i18n.KeyAdminHPDisputesName)) {
				t.Error("an unconfigured Stripe still renders the disputes row")
			}
		})
	}
}

func TestOpenDisputeAmount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		currency string
		amount   int64
		want     string
	}{
		{name: "shop currency", currency: "twd", amount: 129000, want: "NT$1,290"},
		{name: "US dollars", currency: "usd", amount: 5000, want: "USD"},
		{name: "Japanese yen", currency: "jpy", amount: 5000, want: "JPY"},
		{name: "Bahraini dinars", currency: "bhd", amount: 5000, want: "BHD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (OpenDispute{AmountCents: tt.amount, Currency: tt.currency}).Amount(); got != tt.want {
				t.Errorf("Amount(%q, %d) = %q, want %q", tt.currency, tt.amount, got, tt.want)
			}
		})
	}
}

func TestHealthDisputeAmountsDoNotExposeForeignMinorUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		locale i18n.Locale
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant},
		{name: "English", locale: i18n.En},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := &WorkerHealthView{
				Disputes: DisputeState{Configured: true, Items: []OpenDispute{
					{URL: "https://dashboard.stripe.com/disputes/dp_usd", AmountCents: 5000, Currency: "usd"},
					{URL: "https://dashboard.stripe.com/disputes/dp_twd", AmountCents: 129000, Currency: "twd"},
				}},
			}
			html := renderComponent(t, ctx, Health(layouts.Page{Title: "health"}, view))
			for _, want := range []string{
				`<td class="goen-admin__cellnum">USD</td>`,
				`<td class="goen-admin__cellnum">NT$1,290</td>`,
				`href="https://dashboard.stripe.com/disputes/dp_usd"`,
				`href="https://dashboard.stripe.com/disputes/dp_twd"`,
			} {
				if !strings.Contains(html, want) {
					t.Errorf("Health() does not contain %q", want)
				}
			}
			if strings.Contains(html, "5000") {
				t.Error("Health() exposes the foreign dispute's minor-unit figure 5000")
			}
		})
	}
}

// TestHealthKeepsMachineCodesInsideTechnicalDetails: the claim's kind and
// status read in the page's language, and the raw error category and event
// reason appear only inside a closed <details>, for whoever matches the log.
func TestHealthKeepsMachineCodesInsideTechnicalDetails(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &WorkerHealthView{
		StrandedClaims: []StrandedClaim{{
			Operation: "0199aaaa-0000-7000-8000-000000000001", OrderNumber: "GO-261004-000001",
			Kind: "allowance", Status: "attention", LastError: "allowance_multiple_unknown_candidates",
		}},
		UnreconciledEvents: []UnreconciledEvent{{
			EventID: "evt_1", Type: "checkout.session.completed", Ref: "cs_test_1",
			Reason: "cancelled_order_capture: money arrived",
		}},
	}
	html := renderToString(t, Health(layouts.Page{Title: "health"}, view))
	outside := html
	for {
		before, rest, found := strings.Cut(outside, "<details")
		if !found {
			break
		}
		_, after, _ := strings.Cut(rest, "</details>")
		outside = before + after
	}
	for _, code := range []string{"allowance_multiple_unknown_candidates", "cancelled_order_capture", ">allowance<", ">attention<"} {
		if strings.Contains(outside, code) {
			t.Errorf("Health prints %q outside a technical-details disclosure", code)
		}
		if strings.HasPrefix(code, ">") {
			continue
		}
		if !strings.Contains(html, "<code>"+code) {
			t.Errorf("Health does not keep %q for the developer inside the disclosure", code)
		}
	}
	for _, key := range []i18n.Key{i18n.KeyAuditInvoiceAllowance, i18n.KeyAdminTimelineInvoiceAttention} {
		if !strings.Contains(outside, i18n.T(ctx, key)) {
			t.Errorf("Health does not say %q in the claim's row", i18n.T(ctx, key))
		}
	}
}
