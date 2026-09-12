package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPayoutChannelNamesTheFrozenSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		card   int64
		credit int64
		locale i18n.Locale
		want   string
		not    string
	}{
		{
			name: "card-only Traditional Chinese",
			card: 140000, locale: i18n.ZhHant,
			want: "卡款 NT$1,400 走 Stripe", not: "額度",
		},
		{
			name: "card-only English",
			card: 140000, locale: i18n.En,
			want: "Card NT$1,400 refunds through Stripe", not: "store credit",
		},
		{
			name:   "credit-only Traditional Chinese",
			credit: 200000, locale: i18n.ZhHant,
			want: "店儲 NT$2,000 退回額度", not: "Stripe",
		},
		{
			name:   "credit-only English",
			credit: 200000, locale: i18n.En,
			want: "Store credit NT$2,000 returns to the balance", not: "Stripe",
		},
		{
			name: "split Traditional Chinese",
			card: 140000, credit: 60000, locale: i18n.ZhHant,
			want: "卡款 NT$1,400 走 Stripe,店儲 NT$600 退回額度",
		},
		{
			name: "split English",
			card: 140000, credit: 60000, locale: i18n.En,
			want: "Card NT$1,400 through Stripe, store credit NT$600 back to the balance",
		},
		{
			name:   "open request has no frozen channel",
			locale: i18n.ZhHant,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			row := AdminReturn{CardRefundCents: tt.card, CreditRefundCents: tt.credit}
			got := row.PayoutChannel(ctx)
			if got != tt.want {
				t.Errorf("PayoutChannel() = %q, want %q", got, tt.want)
			}
			if tt.not != "" && strings.Contains(got, tt.not) {
				t.Errorf("PayoutChannel() = %q, must not mention %q", got, tt.not)
			}
		})
	}
}

func TestTheReturnQueueHTMLNamesTheRefundChannels(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, locale i18n.Locale, row AdminReturn) string {
		t.Helper()
		var b strings.Builder
		if err := AdminReturns(layouts.Page{Title: "退貨"}, AdminReturnsView{
			Rows: []AdminReturn{row},
		}).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
			t.Fatalf("render: %v", err)
		}
		return b.String()
	}

	t.Run("credit-only English names the ledger, not Stripe", func(t *testing.T) {
		t.Parallel()
		html := render(t, i18n.En, AdminReturn{
			ID: "credit-row", OrderNumber: "GO-CREDIT", Status: "approved",
			StatusText: "Approved", Window: "within", Decided: true,
			CreditRefundCents: 200000,
		})
		want := i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAdminRetPayoutCredit)
		want = strings.ReplaceAll(want, "%s", "NT$2,000")
		if !strings.Contains(html, want) {
			t.Errorf("credit-only HTML lacks %q", want)
		}
		if strings.Contains(html, "refunds through Stripe") {
			t.Error("a credit-only return is described as a Stripe refund")
		}
	})

	t.Run("split Traditional Chinese names both sources", func(t *testing.T) {
		t.Parallel()
		html := render(t, i18n.ZhHant, AdminReturn{
			ID: "split-row", OrderNumber: "GO-SPLIT", Status: "approved",
			StatusText: "已同意", Window: "within", Decided: true,
			CardRefundCents: 140000, CreditRefundCents: 60000,
		})
		want := "卡款 NT$1,400 走 Stripe,店儲 NT$600 退回額度"
		if !strings.Contains(html, want) {
			t.Errorf("split HTML lacks %q", want)
		}
	})

	t.Run("an open request does not invent a channel", func(t *testing.T) {
		t.Parallel()
		html := render(t, i18n.ZhHant, AdminReturn{
			ID: "open-row", OrderNumber: "GO-OPEN", Status: "requested",
			StatusText: "待處理", Window: "within",
		})
		for _, frozen := range []string{
			"卡款 NT$", "店儲 NT$",
			i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutCard),
			i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutCredit),
		} {
			if strings.Contains(html, frozen) {
				t.Errorf("an open request shows a frozen payout channel %q", frozen)
			}
		}
		lead := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetLead)
		if !strings.Contains(html, lead) {
			t.Error("the page lead is absent")
		}
	})
}

func TestAnApprovedReturnWithMoneyOutstandingOffersToSendItAgain(t *testing.T) {
	base := func(id string) AdminReturn {
		return AdminReturn{
			ID: id, OrderNumber: "GO-TEST", Status: "approved", StatusText: "已同意",
			Reason: "不合用", Window: "within",
		}
	}
	render := func(t *testing.T, row AdminReturn) string {
		t.Helper()
		return renderToString(t, AdminReturns(layouts.Page{Title: "退貨"}, AdminReturnsView{
			Rows: []AdminReturn{row},
		}))
	}

	t.Run("an open request offers both decisions", func(t *testing.T) {
		row := base("open-row")
		row.Status = "requested"
		row.Decided = false
		html := render(t, row)
		for _, want := range []string{
			`action="/admin/returns/open-row/decide"`, `value="approved"`, `value="rejected"`,
			`name="resolution"`, `maxlength="300"`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("open request HTML lacks %s", want)
			}
		}
	})

	t.Run("an outstanding payout offers only the retry", func(t *testing.T) {
		row := base("retry-row")
		row.Decided = true
		row.PayoutOutstanding = true
		html := render(t, row)
		for _, want := range []string{
			`method="post"`, `action="/admin/returns/retry-row/decide"`,
			`name="decision"`, `value="approved"`, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetRetryPayout),
		} {
			if !strings.Contains(html, want) {
				t.Errorf("retry HTML lacks %s", want)
			}
		}
		if strings.Contains(html, `value="rejected"`) {
			t.Error("a payout retry offers to retake the rejection decision")
		}
		if strings.Contains(html, `name="resolution"`) {
			t.Error("a payout retry asks for a new decision note which cannot change the approved claim")
		}
	})

	t.Run("a settled payout has no decision action", func(t *testing.T) {
		row := base("settled-row")
		row.Decided = true
		html := render(t, row)
		if strings.Contains(html, `/admin/returns/settled-row/decide`) {
			t.Error("a settled return still offers a decision or payout action")
		}
	})

	t.Run("an inconsistent payout is explained without an unsafe button", func(t *testing.T) {
		row := base("stranded-row")
		row.Decided = true
		row.PayoutOutstanding = true
		row.PayoutBlocked = true
		html := render(t, row)
		stranded := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutStranded)
		if !strings.Contains(html, stranded) {
			t.Error("the inconsistent-payout explanation is absent")
		}
		if strings.Contains(html, `/admin/returns/stranded-row/decide`) {
			t.Error("an inconsistent payout offers a retry that cannot be proven safe")
		}
	})
}
