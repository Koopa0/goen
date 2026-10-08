package email

import (
	"strings"
	"testing"
	"time"
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
// The year is 2099 so the day is never in the shop's current year.
func TestTheArrivalMailStatesTheLastDayToReturn(t *testing.T) {
	t.Parallel()
	day := time.Date(2099, 10, 9, 4, 0, 0, 0, time.UTC)
	want := map[string]string{"zh-Hant": "2099\u00a0年 10\u00a0月 9\u00a0日", "en": "Oct\u00a09, 2099"}
	for _, kind := range []TerminalKind{TerminalDelivered, TerminalCollected} {
		for _, locale := range []string{"en", "zh-Hant"} {
			for _, delivered := range []bool{true, false} {
				n, sink := notifier(t)
				to := TerminalRecipient{
					Address: "reader@example.com", Name: "Reader", Locale: locale,
					OrderNumber: "GO-260101-000001",
				}
				if delivered {
					to.RescissionEnds = day
				}
				if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: kind}, to); err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(sink.msg.Body, want[locale]); got != delivered {
					t.Errorf("%s/%s delivered=%t: body names %q = %t:\n%s", kind, locale, delivered, want[locale], got, sink.msg.Body)
				}
				if strings.Contains(sink.msg.Body, "2099-10-09") {
					t.Errorf("%s/%s: the day is written as ISO:\n%s", kind, locale, sink.msg.Body)
				}
			}
		}
	}
}

// A payment-deadline cancellation carries no rescission sentence, whatever day
// the recipient holds.
func TestACancellationMailSaysNothingOfTheRightToReturn(t *testing.T) {
	t.Parallel()
	for _, locale := range []string{"en", "zh-Hant"} {
		n, sink := notifier(t)
		to := TerminalRecipient{
			Address: "reader@example.com", Name: "Reader", Locale: locale,
			OrderNumber: "GO-260101-000001", RescissionEnds: time.Date(2099, 10, 9, 4, 0, 0, 0, time.UTC),
		}
		if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: TerminalCancelledByPaymentDeadline}, to); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sink.msg.Body, "2099") {
			t.Errorf("%s: a cancellation names a return day:\n%s", locale, sink.msg.Body)
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

// Store credit is returned only to someone who applied it, so a cancellation
// may not claim it was, in either language.
func TestACancellationMentionsStoreCreditOnlyAsAConditional(t *testing.T) {
	t.Parallel()
	for _, kind := range []TerminalKind{TerminalCancelledByCustomer, TerminalCancelledByPaymentDeadline} {
		for _, refunded := range []bool{false, true} {
			for _, locale := range []string{"en", "zh-Hant"} {
				n, sink := notifier(t)
				if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: kind, Refunded: refunded}, TerminalRecipient{Address: "reader@example.com", Locale: locale, OrderNumber: "GO-260101-000001"}); err != nil {
					t.Fatal(err)
				}
				body := sink.msg.Body
				want, bad := "any store credit you applied", ""
				if locale == "zh-Hant" {
					want, bad = "若有使用購物金", "你使用的購物金"
				}
				if !strings.Contains(body, want) {
					t.Errorf("%s/%s refunded=%t: missing %q:\n%s", kind, locale, refunded, want, body)
				}
				if bad != "" && strings.Contains(body, bad) {
					t.Errorf("%s/%s refunded=%t: asserts store credit was used (%q):\n%s", kind, locale, refunded, bad, body)
				}
			}
		}
	}
}

// A shop cancellation that returned money says so; one that returned none
// sends the reader to the order page and promises nothing.
func TestAShopCancellationNamesTheRefundOnlyWhenOneWasMade(t *testing.T) {
	t.Parallel()
	for _, refunded := range []bool{false, true} {
		for _, locale := range []string{"en", "zh-Hant"} {
			n, sink := notifier(t)
			if err := n.SendOrderTerminal(t.Context(), &OrderTerminal{Kind: TerminalCancelledByStaff, Refunded: refunded}, TerminalRecipient{Address: "reader@example.com", Locale: locale, OrderNumber: "GO-260101-000001"}); err != nil {
				t.Fatal(err)
			}
			body := sink.msg.Body
			want, returned, unpromised := "If a payment reached us, it has been or will be refunded in full", "any store credit you applied", "payment and refund status"
			if locale == "zh-Hant" {
				want, returned, unpromised = "若有款項已經到帳，已經或將會全額退還給你", "若有使用購物金", "付款及退款狀態"
			}
			if got := strings.Contains(body, want); got != refunded {
				t.Errorf("%s refunded=%t: names the refund = %t:\n%s", locale, refunded, got, body)
			}
			if got := strings.Contains(body, returned); got != refunded {
				t.Errorf("%s refunded=%t: mentions store credit = %t:\n%s", locale, refunded, got, body)
			}
			if got := strings.Contains(body, unpromised); got == refunded {
				t.Errorf("%s refunded=%t: points at the order page for refund status = %t:\n%s", locale, refunded, got, body)
			}
		}
	}
}
