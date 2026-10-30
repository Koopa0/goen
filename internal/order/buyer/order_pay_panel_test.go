package buyer

import (
	"strings"
	"testing"
	"time"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/frame"
)

// unpaidOrder is a placed order that owes money, holding its stock until 12:37 shop time.
func unpaidOrder() *OrderView {
	held := time.Date(2026, 10, 9, 3, 37, 0, 0, time.UTC)
	return &OrderView{
		Number: "GO-261009-000021", Status: order.FulfillmentPending, OwedCents: 106000, Now: orderNow,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		HoldFrom: held.Add(-time.Hour), HoldUntil: held.Add(time.Hour),
	}
}

// actionsOf is the money panel's action column, which holds the page's next step.
func actionsOf(t *testing.T, html string) *nethtml.Node {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	actions := findDescendant(doc, func(n *nethtml.Node) bool {
		return n.Type == nethtml.ElementNode && strings.Contains(" "+attrValue(n, "class")+" ", " goen-summary__actions ")
	})
	if actions == nil {
		t.Fatal("the order page has no action column")
	}
	return actions
}

// TestOrderPayPanel: an unpaid order's first action is the primary 前往付款 button
// where payments are on and 聯絡我們 where they are off, with the hold's end beside
// it, and the page does not offer the order again. A link inside the notice is not
// the next step, and with payments off the page still needs a way forward.
func TestOrderPayPanel(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		payments bool
		locale   i18n.Locale
		href     string
		label    i18n.Key
	}{
		{"payments on", true, i18n.ZhHant, "/orders/GO-261009-000021/pay", i18n.KeyPay},
		{"payments off", false, i18n.ZhHant, "/contact", i18n.KeyContact},
		{"payments on in english", true, i18n.En, "/orders/GO-261009-000021/pay", i18n.KeyPay},
		{"payments off in english", false, i18n.En, "/contact", i18n.KeyContact},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			v := unpaidOrder()
			v.PaymentsEnabled = tt.payments
			html := renderComponent(t, ctx, Order(frame.Page{Title: "訂單"}, v))
			actions := actionsOf(t, html)

			first := findDescendant(actions, func(n *nethtml.Node) bool {
				return n.Type == nethtml.ElementNode && (n.Data == "a" || n.Data == "button")
			})
			if first == nil || first.Data != "a" || attrValue(first, "href") != tt.href {
				t.Fatalf("the first action is not the link to %q:\n%s", tt.href, nodeText(actions))
			}
			if class := " " + attrValue(first, "class") + " "; !strings.Contains(class, " goen-btn--primary ") {
				t.Errorf("the first action is drawn as %q, want the primary level", class)
			}
			if got, want := strings.TrimSpace(nodeText(first)), i18n.T(ctx, tt.label); got != want {
				t.Errorf("the first action reads %q, want %q", got, want)
			}

			lead, note, ok := payment.PayHold{PlacedAt: v.HoldFrom, Until: v.HoldUntil}.Window(ctx)
			if !ok || !strings.Contains(lead, "12:37") {
				t.Fatalf("the pay page's own hold wording %q is not the 12:37 the order holds to", lead)
			}
			if text := nodeText(actions); !strings.Contains(text, lead) || !strings.Contains(text, note) {
				t.Errorf("the action column lacks the pay page's hold wording %q / %q:\n%s", lead, note, text)
			}
			if strings.Contains(html, `action="/orders/GO-261009-000021/reorder"`) || strings.Contains(html, i18n.T(ctx, i18n.KeyOrderReorder)) {
				t.Error("an unpaid order offers to buy the same goods again")
			}
			if got := strings.Contains(html, `/pay"`); got != tt.payments {
				t.Errorf("the link to the pay page renders = %v, want %v", got, tt.payments)
			}
		})
	}
}

// TestOrderPayPanelHolds: the panel names a deadline only where the order has a
// stored hold, and offers no payment, and no second purchase, to a shopper just
// back from paying.
func TestOrderPayPanelHolds(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	reorder := `action="/orders/GO-261009-000021/reorder"`

	noHold := unpaidOrder()
	noHold.PaymentsEnabled = true
	noHold.HoldFrom, noHold.HoldUntil = time.Time{}, time.Time{}
	html := renderComponent(t, ctx, Order(frame.Page{Title: "訂單"}, noHold))
	if strings.Contains(html, "商品保留到") || !strings.Contains(html, `href="/orders/GO-261009-000021/pay"`) {
		t.Error("an order with no stored hold names a deadline or loses its pay button")
	}

	for name, back := range map[string]func(*OrderView){
		"checking": func(v *OrderView) { v.PaymentRefreshURL = "/orders/GO-261009-000021?paid=1&confirmation=1" },
		"ended":    func(v *OrderView) { v.PaymentConfirmationPending = true },
	} {
		v := unpaidOrder()
		v.PaymentsEnabled = true
		back(v)
		html := renderComponent(t, ctx, Order(frame.Page{Title: "訂單"}, v))
		if strings.Contains(html, `href="/orders/GO-261009-000021/pay"`) || strings.Contains(html, reorder) || strings.Contains(html, "商品保留到") {
			t.Errorf("%s: a shopper just back from paying is offered to pay or buy again", name)
		}
	}
}

// TestOrderReorderGate: 再買一次 is offered once the order is paid, completed or
// cancelled, and not while it is still waiting for payment, where it would put the
// same goods in the cart beside an order that holds them.
func TestOrderReorderGate(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	for _, tt := range []struct {
		name string
		view OrderView
		want bool
	}{
		{"unpaid with payments on", OrderView{Status: order.FulfillmentPending, OwedCents: 106000, PaymentsEnabled: true}, false},
		{"unpaid with payments off", OrderView{Status: order.FulfillmentPending, OwedCents: 106000}, false},
		{"paid, not yet picked", OrderView{Status: order.FulfillmentPending, Committed: true, OwedCents: 106000}, true},
		{"paid from store credit", OrderView{Status: order.FulfillmentPending, CreditCents: 106000}, true},
		{"delivered", OrderView{Status: order.FulfillmentDelivered, Committed: true, OwedCents: 106000}, true},
		{"completed", OrderView{Status: order.FulfillmentCompleted, Committed: true, OwedCents: 106000}, true},
		{"cancelled", OrderView{Status: order.FulfillmentCancelled, OwedCents: 106000}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := tt.view
			v.Number, v.Now = "GO-261009-000021", orderNow
			html := renderComponent(t, ctx, Order(frame.Page{Title: "訂單"}, &v))
			if got := strings.Contains(html, `action="/orders/GO-261009-000021/reorder"`); got != tt.want {
				t.Errorf("the reorder form renders = %v, want %v", got, tt.want)
			}
		})
	}
}
