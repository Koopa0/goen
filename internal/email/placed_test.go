package email

import (
	"strings"
	"testing"
	"time"
)

func TestPlacedConfirmationKeepsTheRecordedOrderTerms(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		tag                       string
		facts                     []string
		card, mixed, credit, zero string
		deadline                  string
	}{
		{"en", []string{"Order details at placement:", "TEA-2", "Tea <box>", "Large", "2 × NT$1,250 = NT$2,500", "Subtotal: NT$2,500", "Delivery: NT$60", "Discount: NT$100", "Tax: NT$0", "Total: NT$2,460", "Home delivery", "Recipient: Alex", "Phone: 0912345678", "110 Taipei Road 1"}, "Credit card: NT$2,460 remains payable.", "Store credit applied: NT$1,000. Credit card: NT$1,460 remains payable.", "Paid with store credit: NT$2,460.", "No payment required.", "Begin payment before 2099-01-01 08:29 (Taipei time)."},
		{"zh-Hant", []string{"下單時的訂單內容：", "TEA-2", "Tea <box>", "Large", "2 × NT$1,250 = NT$2,500", "小計：NT$2,500", "運費：NT$60", "折扣：NT$100", "稅額：NT$0", "應付金額：NT$2,460", "Home delivery", "收件人：Alex", "電話：0912345678", "110 Taipei Road 1"}, "信用卡：尚須支付 NT$2,460。", "購物金已折抵 NT$1,000；信用卡尚須支付 NT$1,460。", "已使用購物金支付 NT$2,460。", "無須付款。", "請在 2099-01-01 08:29（台北時間）前開始付款。"},
	} {
		for _, funding := range []struct {
			name                string
			total, credit, owed int64
			want                string
			deadline            bool
		}{
			{"card", 246000, 0, 246000, locale.card, true},
			{"mixed", 246000, 100000, 146000, locale.mixed, true},
			{"credit", 246000, 246000, 0, locale.credit, false},
			{"zero", 0, 0, 0, locale.zero, false},
		} {
			t.Run(locale.tag+"/"+funding.name, func(t *testing.T) {
				t.Parallel()
				n, sink := notifier(t)
				p := recordedPlacedOrder(locale.tag)
				p.TotalCents, p.Snapshot.CreditCents, p.OwedCents = funding.total, funding.credit, centsPtr(funding.owed)
				if funding.total == 0 {
					p.Snapshot.SubtotalCents, p.Snapshot.ShippingCents, p.Snapshot.DiscountCents = 0, 0, 0
					p.Snapshot.Lines[0].UnitCents = 0
				}
				if err := n.SendOrderPlaced(t.Context(), p); err != nil {
					t.Fatalf("SendOrderPlaced() = %v", err)
				}
				for _, want := range append([]string{funding.want, "https://goen.test/orders/GO-990101-000001"}, locale.facts...) {
					if funding.total == 0 && strings.Contains(want, "NT$") {
						continue
					}
					if !strings.Contains(sink.msg.Body, want) {
						t.Errorf("SendOrderPlaced() body missing %q:\n%s", want, sink.msg.Body)
					}
				}
				if got := strings.Contains(sink.msg.Body, locale.deadline); got != funding.deadline {
					t.Errorf("SendOrderPlaced() payment deadline present = %t, want %t", got, funding.deadline)
				}
				if !strings.Contains(sink.msg.HTML, "Tea &lt;box&gt;") || strings.Contains(sink.msg.HTML, "Tea <box>") {
					t.Errorf("SendOrderPlaced() HTML does not escape the recorded product name: %s", sink.msg.HTML)
				}
			})
		}
	}
}

func TestADelayedPlacedConfirmationDoesNotInviteStartingAnExpiredPayment(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ locale, want, forbidden string }{
		{"en", "The deadline to begin payment has passed. Check the order's current status before attempting payment.", "Begin payment before"},
		{"zh-Hant", "開始付款的期限已過，請先查看訂單的目前狀態，再確認能否付款。", "前開始付款"},
	} {
		t.Run(tt.locale, func(t *testing.T) {
			t.Parallel()
			n, sink := notifier(t)
			p := recordedPlacedOrder(tt.locale)
			p.Snapshot.HoldUntil = time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)
			p.Snapshot.StartBy = time.Date(2000, 1, 1, 0, 29, 0, 0, time.UTC)
			if err := n.SendOrderPlaced(t.Context(), p); err != nil {
				t.Fatalf("SendOrderPlaced() = %v", err)
			}
			for _, want := range []string{tt.want, "2000-01-01 08:29", "2000-01-01 09:00"} {
				if !strings.Contains(sink.msg.Body, want) {
					t.Errorf("SendOrderPlaced() late body missing %q:\n%s", want, sink.msg.Body)
				}
			}
			if strings.Contains(sink.msg.Body, tt.forbidden) {
				t.Errorf("SendOrderPlaced() late body invites expired payment:\n%s", sink.msg.Body)
			}
		})
	}
}

func recordedPlacedOrder(locale string) *OrderPlaced {
	return &OrderPlaced{Locale: locale, OrderNumber: "GO-990101-000001", Email: "alex@example.com", Name: "Alex", TotalCents: 246000, OwedCents: centsPtr(246000), Snapshot: &PlacedSnapshot{
		Lines:         []PlacedLine{{SKU: "TEA-2", Name: "Tea <box>", Label: "Large", UnitCents: 125000, Quantity: 2}},
		SubtotalCents: 250000, ShippingCents: 6000, DiscountCents: 10000, ShippingName: "Home delivery", DeliveryTo: "110 Taipei Road 1", Phone: "0912345678",
		HoldUntil: time.Date(2099, 1, 1, 1, 0, 0, 0, time.UTC), StartBy: time.Date(2099, 1, 1, 0, 29, 0, 0, time.UTC),
	}}
}
