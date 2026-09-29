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
		{TerminalCancelledByCustomer, "You cancelled", "您已取消"},
		{TerminalCancelledByStaff, "The shop cancelled", "商店已取消"},
		{TerminalDelivered, "marked as delivered", "已標記為送達"},
		{TerminalCollected, "marked as collected", "已標記為取貨完成"},
	}
	for _, tc := range cases {
		for _, locale := range []string{"en", "zh-Hant"} {
			n, sink := notifier(t)
			if err := n.SendOrderTerminal(t.Context(), tc.kind, TerminalRecipient{Address: "reader@example.com", Name: "Reader", Locale: locale, OrderNumber: "GO-260101-000001"}); err != nil {
				t.Fatal(err)
			}
			want := tc.en
			if locale == "zh-Hant" {
				want = tc.zh
			}
			if sink.msg == nil || !strings.Contains(sink.msg.Body, want) || !strings.Contains(sink.msg.Body, "GO-260101-000001") {
				t.Fatalf("%s/%s notice=%+v", tc.kind, locale, sink.msg)
			}
			if tc.kind == TerminalCancelledByCustomer && (strings.Contains(sink.msg.Body, "refund") || strings.Contains(sink.msg.Body, "退款")) {
				t.Fatalf("an unpaid order's cancellation points at a refund: %s", sink.msg.Body)
			}
			if strings.Contains(sink.msg.Body, "has been refunded") || strings.Contains(sink.msg.Body, "已退款") {
				t.Fatal("cancellation promised a refund")
			}
		}
	}
}
