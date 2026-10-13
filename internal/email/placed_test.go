package email

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
)

func TestPlacedConfirmationKeepsTheRecordedOrderTerms(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		tag      string
		facts    []string
		rows     [][]string
		funding  []string
		deadline string
	}{
		{
			tag:   "en",
			facts: []string{"TEA-2", "Tea <box>", "Large", "Home delivery", "Recipient: Alex", "Phone: 0912345678", "110 Taipei Road 1", "Your note: Leave <inside>"},
			rows: [][]string{
				{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Discount: -NT$100", "Total: NT$2,460"},
				{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Discount: -NT$100", "Paid with store credit: -NT$1,000", "Amount due: NT$1,460"},
				{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Discount: -NT$100", "Paid with store credit: -NT$2,460", "Amount due: NT$0"},
				{"Subtotal: NT$0", "Delivery (Home delivery): Free", "Total: NT$0"},
			},
			funding:  []string{"Credit card: NT$2,460 remains payable.", "Store credit applied: NT$1,000. Credit card: NT$1,460 remains payable.", "Paid with store credit: NT$2,460.", "No payment required."},
			deadline: "Start paying by 2099-01-01 08:29",
		},
		{
			tag:   "zh-Hant",
			facts: []string{"TEA-2", "Tea <box>", "Large", "Home delivery", "收件人：Alex", "電話：0912345678", "110 Taipei Road 1", "備註：Leave <inside>"},
			rows: [][]string{
				{"小計：NT$2,500", "運費（Home delivery）：NT$60", "折扣：-NT$100", "總計：NT$2,460"},
				{"小計：NT$2,500", "運費（Home delivery）：NT$60", "折扣：-NT$100", "購物金折抵：-NT$1,000", "應付：NT$1,460"},
				{"小計：NT$2,500", "運費（Home delivery）：NT$60", "折扣：-NT$100", "購物金折抵：-NT$2,460", "應付：NT$0"},
				{"小計：NT$0", "運費（Home delivery）：免運", "總計：NT$0"},
			},
			funding:  []string{"信用卡：尚須支付 NT$2,460。", "購物金已折抵 NT$1,000；信用卡尚須支付 NT$1,460。", "已使用購物金支付 NT$2,460。", "無須付款。"},
			deadline: "請在 2099-01-01 08:29 前開始付款",
		},
	} {
		for index, funding := range []struct {
			name                string
			total, credit, owed int64
			deadline            bool
		}{
			{name: "card", total: 246000, owed: 246000, deadline: true},
			{name: "mixed", total: 246000, credit: 100000, owed: 146000, deadline: true},
			{name: "credit", total: 246000, credit: 246000},
			{name: "zero"},
		} {
			t.Run(locale.tag+"/"+funding.name, func(t *testing.T) {
				t.Parallel()
				n, sink := notifier(t)
				p := recordedPlacedOrder(locale.tag)
				p.TotalCents, p.Snapshot.CreditCents, p.OwedCents = funding.total, funding.credit, centsPtr(funding.owed)
				line := "2 × NT$1,250 = NT$2,500"
				if funding.total == 0 {
					p.Snapshot.SubtotalCents, p.Snapshot.ShippingCents, p.Snapshot.DiscountCents = 0, 0, 0
					p.Snapshot.Lines[0].UnitCents = 0
					line = "2 × NT$0 = NT$0"
				}
				if err := n.SendOrderPlaced(t.Context(), p); err != nil {
					t.Fatalf("SendOrderPlaced() = %v", err)
				}
				wantFacts := append([]string{locale.funding[index], line, "https://goen.test/orders/GO-990101-000001"}, locale.facts...)
				for _, want := range wantFacts {
					if !strings.Contains(sink.msg.Body, want) {
						t.Errorf("SendOrderPlaced() body missing %q:\n%s", want, sink.msg.Body)
					}
				}
				if diff := cmp.Diff(locale.rows[index], placedAmountRows(t, sink.msg.Body)); diff != "" {
					t.Errorf("SendOrderPlaced() summary mismatch (-want +got):\n%s", diff)
				}
				if got := strings.Contains(sink.msg.Body, locale.deadline); got != funding.deadline {
					t.Errorf("SendOrderPlaced() payment deadline present = %t, want %t", got, funding.deadline)
				}
				for _, forbidden := range []string{"Customer's note", "顧客備註", "Tax:", "稅額：", "Taipei time", "台北時間", "stock hold", "Begin payment"} {
					if strings.Contains(sink.msg.Body, forbidden) {
						t.Errorf("SendOrderPlaced() body contains forbidden %q:\n%s", forbidden, sink.msg.Body)
					}
				}
				for _, text := range []struct{ escaped, raw string }{
					{"Tea &lt;box&gt;", "Tea <box>"},
					{"Leave &lt;inside&gt;", "Leave <inside>"},
				} {
					if !strings.Contains(sink.msg.HTML, text.escaped) || strings.Contains(sink.msg.HTML, text.raw) {
						t.Errorf("SendOrderPlaced() HTML does not escape %q: %s", text.raw, sink.msg.HTML)
					}
				}
			})
		}
	}
}

func TestPlacedConfirmationDiscountRowsFollowTheRecordedReason(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		locale   string
		reason   string
		discount int64
		total    int64
		rows     []string
	}{
		{name: "en default", locale: "en", discount: 10000, total: 246000, rows: []string{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Discount: -NT$100", "Total: NT$2,460"}},
		{name: "en reason", locale: "en", reason: "WELCOME", discount: 10000, total: 246000, rows: []string{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Discount (WELCOME): -NT$100", "Total: NT$2,460"}},
		{name: "en absent", locale: "en", reason: "WELCOME", total: 256000, rows: []string{"Subtotal: NT$2,500", "Delivery (Home delivery): NT$60", "Total: NT$2,560"}},
		{name: "zh default", locale: "zh-Hant", discount: 10000, total: 246000, rows: []string{"小計：NT$2,500", "運費（Home delivery）：NT$60", "折扣：-NT$100", "總計：NT$2,460"}},
		{name: "zh reason", locale: "zh-Hant", reason: "WELCOME", discount: 10000, total: 246000, rows: []string{"小計：NT$2,500", "運費（Home delivery）：NT$60", "折扣（WELCOME）：-NT$100", "總計：NT$2,460"}},
		{name: "zh absent", locale: "zh-Hant", reason: "WELCOME", total: 256000, rows: []string{"小計：NT$2,500", "運費（Home delivery）：NT$60", "總計：NT$2,560"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n, sink := notifier(t)
			p := recordedPlacedOrder(tt.locale)
			p.Snapshot.DiscountReason, p.Snapshot.DiscountCents = tt.reason, tt.discount
			p.TotalCents, p.OwedCents = tt.total, centsPtr(tt.total)
			if err := n.SendOrderPlaced(t.Context(), p); err != nil {
				t.Fatalf("SendOrderPlaced() = %v", err)
			}
			if diff := cmp.Diff(tt.rows, placedAmountRows(t, sink.msg.Body)); diff != "" {
				t.Errorf("SendOrderPlaced() discount summary mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPlacedConfirmationDeadlineIncludesTheLastAdmittedInstant(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		tag        string
		invitation string
		expired    string
		reserved   string
		zone       string
	}{
		{tag: "en", invitation: "Start paying by 2099-01-01 08:29", expired: "has passed.", reserved: "reserved until 2099-01-01 09:00", zone: "Taiwan time"},
		{tag: "zh-Hant", invitation: "請在 2099-01-01 08:29 前開始付款", expired: "開始付款的期限已過", reserved: "商品保留到 2099-01-01 09:00", zone: "台灣時間"},
	} {
		for _, boundary := range []struct {
			name    string
			offset  time.Duration
			expired bool
		}{
			{name: "before", offset: -time.Nanosecond},
			{name: "equal"},
			{name: "after", offset: time.Nanosecond, expired: true},
		} {
			t.Run(locale.tag+"/"+boundary.name, func(t *testing.T) {
				t.Parallel()
				n, _ := notifier(t)
				p := recordedPlacedOrder(locale.tag)
				ctx := i18n.WithLocale(t.Context(), i18n.Parse(locale.tag))
				body := n.placedConfirmation(ctx, p, 246000, p.Snapshot.StartBy.Add(boundary.offset))
				if got := strings.Contains(body, locale.expired); got != boundary.expired {
					t.Errorf("placedConfirmation(%s) expired = %t, want %t:\n%s", boundary.name, got, boundary.expired, body)
				}
				if got := strings.Contains(body, locale.invitation); got == boundary.expired {
					t.Errorf("placedConfirmation(%s) invitation = %t, want %t:\n%s", boundary.name, got, !boundary.expired, body)
				}
				for _, want := range []string{"2099-01-01 08:29", "2099-01-01 09:00", locale.zone, "https://goen.test/orders/GO-990101-000001"} {
					if !strings.Contains(body, want) {
						t.Errorf("placedConfirmation(%s) missing %q:\n%s", boundary.name, want, body)
					}
				}
				if !boundary.expired && !strings.Contains(body, locale.reserved) {
					t.Errorf("placedConfirmation(%s) missing %q:\n%s", boundary.name, locale.reserved, body)
				}
				for _, forbidden := range []string{"Taipei time", "台北時間", "stock hold", "Begin payment"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("placedConfirmation(%s) contains %q:\n%s", boundary.name, forbidden, body)
					}
				}
			})
		}
	}
}

func TestADelayedPlacedConfirmationDoesNotInviteStartingAnExpiredPayment(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ locale, want, forbidden string }{
		{"en", "Check the order's current status before attempting payment.", "Start paying by"},
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

func placedAmountRows(t *testing.T, body string) []string {
	t.Helper()
	for _, block := range strings.Split(body, "\n\n") {
		for _, prefix := range []string{"Subtotal: ", "小計："} {
			if start := strings.Index(block, prefix); start >= 0 {
				return strings.Split(block[start:], "\n")
			}
		}
	}
	t.Fatalf("placed confirmation has no subtotal summary:\n%s", body)
	return nil
}

func recordedPlacedOrder(locale string) *OrderPlaced {
	return &OrderPlaced{Locale: locale, OrderNumber: "GO-990101-000001", Email: "alex@example.com", Name: "Alex", TotalCents: 246000, OwedCents: centsPtr(246000), Snapshot: &PlacedSnapshot{
		Lines:         []PlacedLine{{SKU: "TEA-2", Name: "Tea <box>", Label: "Large", UnitCents: 125000, Quantity: 2}},
		SubtotalCents: 250000, ShippingCents: 6000, DiscountCents: 10000, ShippingName: "Home delivery", DeliveryTo: "110 Taipei Road 1", Phone: "0912345678", DeliveryNote: "Leave <inside>",
		HoldUntil: time.Date(2099, 1, 1, 1, 0, 0, 0, time.UTC), StartBy: time.Date(2099, 1, 1, 0, 29, 0, 0, time.UTC),
	}}
}
