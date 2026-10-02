package email

import (
	"strings"
	"testing"
)

func TestTerminalNoticesDistinguishActorAndReceiptInBothLanguages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind   TerminalKind
		en, zh string
	}{
		{TerminalCancelledByCustomer, "You cancelled", "你已取消"},
		{TerminalCancelledByStaff, "The shop cancelled", "商店已取消"},
		{TerminalCancelledByPaymentDeadline, "cancelled automatically", "已自動取消"},
		{TerminalDelivered, "marked as delivered", "已標記為送達"},
		{TerminalCollected, "marked as collected", "已標記為取貨完成"},
	}
	for _, tc := range cases {
		for _, locale := range []string{"en", "zh-Hant"} {
			n, sink := notifier(t)
			if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: tc.kind}, TerminalRecipient{Address: "reader@example.com", Name: "Reader", Locale: locale, OrderNumber: "GO-260101-000001"}); err != nil {
				t.Fatal(err)
			}
			want := tc.en
			if locale == "zh-Hant" {
				want = tc.zh
			}
			if sink.msg == nil || !strings.Contains(sink.msg.Body, want) || !strings.Contains(sink.msg.Body, "GO-260101-000001") {
				t.Fatalf("%s/%s notice=%+v", tc.kind, locale, sink.msg)
			}
			if (tc.kind == TerminalCancelledByCustomer || tc.kind == TerminalCancelledByPaymentDeadline) && (strings.Contains(sink.msg.Body, "refund") || strings.Contains(sink.msg.Body, "退款")) {
				t.Fatalf("an unpaid order's cancellation points at a refund: %s", sink.msg.Body)
			}
			if strings.Contains(sink.msg.Body, "has been refunded") || strings.Contains(sink.msg.Body, "已退款") {
				t.Fatal("cancellation promised a refund")
			}
		}
	}
}

// An unpaid order can still have had money reach Stripe after its hold lapsed.
// Its cancellation must say that money comes back, and must not say nothing
// was charged; without that money it says nothing was charged and names no
// refund.
func TestACancellationSaysNothingWasChargedOnlyWhenNoMoneyArrived(t *testing.T) {
	t.Parallel()
	noCharge := map[string][]string{"en": {"not charged", "Nothing was charged"}, "zh-Hant": {"尚未收款", "沒有收取任何款項"}}
	refund := map[string]string{"en": "refunded in full", "zh-Hant": "全額退還"}
	for _, kind := range []TerminalKind{TerminalCancelledByCustomer, TerminalCancelledByPaymentDeadline} {
		for _, refunded := range []bool{false, true} {
			for _, locale := range []string{"en", "zh-Hant"} {
				n, sink := notifier(t)
				if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: kind, Refunded: refunded}, TerminalRecipient{Address: "reader@example.com", Name: "Reader", Locale: locale, OrderNumber: "GO-260101-000001"}); err != nil {
					t.Fatal(err)
				}
				body := sink.msg.Body
				saysNoCharge := false
				for _, phrase := range noCharge[locale] {
					saysNoCharge = saysNoCharge || strings.Contains(body, phrase)
				}
				if saysNoCharge == refunded {
					t.Errorf("%s/%s refunded=%t: says nothing was charged = %t:\n%s", kind, locale, refunded, saysNoCharge, body)
				}
				if strings.Contains(body, refund[locale]) != refunded {
					t.Errorf("%s/%s refunded=%t: names the refund = %t:\n%s", kind, locale, refunded, !refunded, body)
				}
			}
		}
	}
}

// The delivery and collection mails state the last day of the statutory right
// of return when the order has a delivered parcel, and no day when it has not.
func TestTheArrivalMailStatesTheLastDayToReturn(t *testing.T) {
	t.Parallel()
	for _, kind := range []TerminalKind{TerminalDelivered, TerminalCollected} {
		for _, locale := range []string{"en", "zh-Hant"} {
			for _, day := range []string{"2026-10-09", ""} {
				n, sink := notifier(t)
				to := TerminalRecipient{
					Address: "reader@example.com", Name: "Reader", Locale: locale,
					OrderNumber: "GO-260101-000001", RescissionEnds: day,
				}
				if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: kind}, to); err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(sink.msg.Body, "2026-10-09"); got != (day != "") {
					t.Errorf("%s/%s day=%q: body names the day = %t:\n%s", kind, locale, day, got, sink.msg.Body)
				}
			}
		}
	}
}

// A delivered or collected order's subject says which, in both languages: the
// words "receipt" and "update" read like a payment receipt.
func TestTheArrivalSubjectSaysWhatHappened(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind   TerminalKind
		locale string
		want   string
	}{
		{TerminalDelivered, "en", "Order GO-260101-000001 has been delivered"},
		{TerminalCollected, "en", "Order GO-260101-000001 has been collected"},
		{TerminalDelivered, "zh-Hant", "訂單 GO-260101-000001 已送達"},
		{TerminalCollected, "zh-Hant", "訂單 GO-260101-000001 已取貨"},
	} {
		n, sink := notifier(t)
		to := TerminalRecipient{Address: "reader@example.com", Locale: tt.locale, OrderNumber: "GO-260101-000001"}
		if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: tt.kind}, to); err != nil {
			t.Fatal(err)
		}
		if sink.msg.Subject != tt.want {
			t.Errorf("%s/%s subject = %q, want %q", tt.kind, tt.locale, sink.msg.Subject, tt.want)
		}
	}
}
