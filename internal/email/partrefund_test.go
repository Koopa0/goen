package email

import (
	"strings"
	"testing"
)

// The owner's wording for a unit that cannot be sent (#1965), with the
// letter's values in place. A refund paid from one source names only that one.
func TestThePartRefundLetterSaysWhatCameBackAndHow(t *testing.T) {
	t.Parallel()
	notebook := []PartRefundItem{{Name: "點線筆記本", Label: "A5", Quantity: 2}}
	for _, tc := range []struct {
		name, locale  string
		card, credit  int64
		items         []PartRefundItem
		want, without []string
	}{
		{
			name: "card and credit", locale: "zh-Hant", card: 60000, credit: 32600, items: notebook,
			want: []string{"很抱歉，你訂單 GO-260101-000001 中的 點線筆記本 · A5 × 2 無法寄出。這部分的款項 NT$926 已退還：信用卡 NT$600（入帳時間由發卡銀行決定，通常需數個工作天），購物金 NT$326 已回到你的餘額。其他商品照常出貨。若少了這件商品，其他商品對你已沒有用處，收到後七天內可以免費退貨，原付運費也會一併退還。"},
		},
		{
			name: "card and credit", locale: "en", card: 60000, credit: 32600, items: notebook,
			want: []string{"Sorry — 點線筆記本 · A5 × 2 from order GO-260101-000001 cannot be sent. We have refunded NT$926 for it: NT$600 to your card (your issuer decides when it lands, usually a few working days) and NT$326 to your store credit. The rest of your order ships as normal. If the rest is no use to you without it, you can return it free within seven days of receiving it, and the delivery fee comes back too."},
		},
		{
			name: "card only", locale: "zh-Hant", card: 92600, items: notebook,
			want:    []string{"這部分的款項 NT$926 已退還：信用卡 NT$926（入帳時間由發卡銀行決定，通常需數個工作天）。其他商品照常出貨。"},
			without: []string{"購物金"},
		},
		{
			name: "credit only", locale: "en", credit: 92600, items: notebook,
			want:    []string{"cannot be sent. We have refunded NT$926 for it: NT$926 to your store credit. The rest of your order"},
			without: []string{"card"},
		},
		{
			name: "nothing was paid for them", locale: "en", items: notebook,
			want:    []string{"cannot be sent. The rest of your order ships as normal."},
			without: []string{"refunded", "NT$"},
		},
		{
			name: "two lines", locale: "zh-Hant", card: 50000,
			items: []PartRefundItem{{Name: "鋼筆", Quantity: 1}, {Name: "點線筆記本", Label: "A5", Quantity: 2}},
			want:  []string{"中的 鋼筆 × 1、點線筆記本 · A5 × 2 無法寄出。"},
		},
	} {
		t.Run(tc.name+" "+tc.locale, func(t *testing.T) {
			t.Parallel()
			n, sink := notifier(t)
			if err := n.SendOrderPartRefunded(t.Context(), &OrderPartRefunded{}, &PartRefundLetter{
				Address: "reader@example.com", Name: "Reader", Locale: tc.locale, OrderNumber: "GO-260101-000001",
				Items: tc.items, RefundCents: tc.card + tc.credit, CardCents: tc.card, CreditCents: tc.credit,
			}); err != nil {
				t.Fatal(err)
			}
			body := sink.msg.Body
			for _, w := range append(tc.want, "/orders/GO-260101-000001") {
				if !strings.Contains(body, w) {
					t.Errorf("letter lacks %q:\n%s", w, body)
				}
			}
			for _, w := range tc.without {
				if strings.Contains(body, w) {
					t.Errorf("letter names %q:\n%s", w, body)
				}
			}
			if !strings.Contains(sink.msg.Subject, "GO-260101-000001") {
				t.Errorf("subject %q does not name the order", sink.msg.Subject)
			}
		})
	}
}

func TestThePartRefundLetterNeedsARecipientAndItems(t *testing.T) {
	t.Parallel()
	n, sink := notifier(t)
	for name, l := range map[string]*PartRefundLetter{
		"no address": {OrderNumber: "GO-260101-000001", Items: []PartRefundItem{{Name: "pen", Quantity: 1}}},
		"no items":   {Address: "reader@example.com", OrderNumber: "GO-260101-000001"},
	} {
		if err := n.SendOrderPartRefunded(t.Context(), &OrderPartRefunded{}, l); err == nil {
			t.Errorf("%s: sent %+v", name, sink.msg)
		}
	}
}
