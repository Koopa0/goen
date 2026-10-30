package buyer

import (
	"slices"
	"strings"
	"testing"
	"time"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestOrderEvent_LabelKey: LabelKey gives each of the nine event kinds its
// Traditional Chinese history label.
func TestOrderEvent_LabelKey(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for kind, want := range map[order.EventKind]string{
		order.EventPlaced:    "送出訂單",
		order.EventPaid:      "付款完成",
		order.EventPicking:   "開始備貨",
		order.EventShipped:   "出貨",
		order.EventInTransit: "運送中",
		order.EventDelivered: "送達",
		order.EventCompleted: "訂單完成",
		order.EventCancelled: "取消",
		order.EventRefunded:  "已退款",
	} {
		if got := i18n.T(ctx, OrderEvent{Kind: kind}.LabelKey()); got != want {
			t.Errorf("LabelKey(%s) reads %q, want %q", kind, got, want)
		}
	}
}

// TestOrderEvent_LabelKeyUnsent: the refund event of units the shop could not
// send takes the not-sent-and-refunded label in both locales, and a return's
// refund does not.
func TestOrderEvent_LabelKeyUnsent(t *testing.T) {
	t.Parallel()
	for locale, want := range map[i18n.Locale]string{i18n.ZhHant: "無法出貨，已退款", i18n.En: "Not sent — refunded"} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded, Unsent: true}.LabelKey()); got != want {
			t.Errorf("%s: the refund of unsent units reads %q, want %q", locale, got, want)
		}
		if got := i18n.T(ctx, OrderEvent{Kind: order.EventRefunded}.LabelKey()); got == want {
			t.Errorf("%s: a return's refund reads %q too", locale, got)
		}
	}
}

// TestWithCreditPayment: an order store credit paid in full gets a paid entry right after its
// placement, dated by it; a payment event the order already has is not added to, and with no
// placement on record the entry opens the history.
func TestWithCreditPayment(t *testing.T) {
	t.Parallel()
	placedAt := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	placed := OrderEvent{Kind: order.EventPlaced, At: placedAt}
	picking := OrderEvent{Kind: order.EventPicking, At: placedAt.Add(time.Hour)}
	credit := OrderEvent{Kind: order.EventPaid, At: placedAt, ByCredit: true}
	stored := OrderEvent{Kind: order.EventPaid, At: placedAt.Add(time.Minute)}

	for _, tt := range []struct {
		name   string
		events []OrderEvent
		want   []OrderEvent
	}{
		{"after placement", []OrderEvent{placed, picking}, []OrderEvent{placed, credit, picking}},
		{"nothing but placement", []OrderEvent{placed}, []OrderEvent{placed, credit}},
		{"no placement on record", []OrderEvent{picking}, []OrderEvent{credit, picking}},
		{"a payment event is stored", []OrderEvent{placed, stored, picking}, []OrderEvent{placed, stored, picking}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := withCreditPayment(slices.Clone(tt.events), placedAt)
			if !slices.Equal(got, tt.want) {
				t.Errorf("withCreditPayment(%v) = %v, want %v", tt.events, got, tt.want)
			}
		})
	}
}

// TestOrderCreditPaidHistory: the order page prints a credit-paid entry as 付款完成 with the note
// 以購物金付款 after 送出訂單, in both locales, though no note is stored for it.
func TestOrderCreditPaidHistory(t *testing.T) {
	t.Parallel()
	placedAt := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	for locale, want := range map[i18n.Locale][]string{
		i18n.ZhHant: {"送出訂單", "付款完成", "以購物金付款"},
		i18n.En:     {"Order placed", "Paid", "Paid with store credit"},
	} {
		v := &OrderView{
			Number: "GO-261009-000041", Status: order.FulfillmentPending, CreditCents: 106000, ShippingName: "宅配",
			SubtotalCents: 100000, ShippingCents: 6000, PlacedAt: placedAt, Now: orderNow,
			Timeline: withCreditPayment([]OrderEvent{{Kind: order.EventPlaced, At: placedAt}}, placedAt),
		}
		doc, err := nethtml.Parse(strings.NewReader(renderIn(t, locale, Order(frame.Page{Title: "order"}, v))))
		if err != nil {
			t.Fatal(err)
		}
		history := findDescendant(doc, func(n *nethtml.Node) bool {
			return n.Type == nethtml.ElementNode && strings.Contains(" "+attrValue(n, "class")+" ", " goen-order__steps ")
		})
		if history == nil {
			t.Fatalf("%s: the order page has no history", locale)
		}
		text, from := nodeText(history), 0
		for _, w := range want {
			i := strings.Index(text[from:], w)
			if i < 0 {
				t.Fatalf("%s: the history lacks %q after offset %d:\n%s", locale, w, from, text)
			}
			from += i + len(w)
		}
	}
}
