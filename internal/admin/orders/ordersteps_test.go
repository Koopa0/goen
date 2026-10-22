package orders

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestOrderNextStepSummary: the next-step summary uses the first applicable
// fact.
func TestOrderNextStepSummary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		view   OrderView
		zh, en string
	}{
		{"refund before other work", OrderView{RefundOpen: true, AllowanceAttention: "attention", AllowanceAwaitingUntil: "2026-10-13 12:00", Unpaid: true, Status: order.FulfillmentPending}, "出貨前退款還沒完成，請在退款區按「繼續退款」。", "The refund before shipment is unfinished; choose “Resume the refund” in the refund section."},
		{"invoice attention before consent", OrderView{AllowanceAttention: "attention", AllowanceAwaitingUntil: "2026-10-13 12:00", Unpaid: true, Status: order.FulfillmentPending}, "發票折讓需要人工處理，請看發票區。", "The invoice allowance needs staff attention; see the invoice section."},
		{"consent before payment", OrderView{AllowanceAwaitingUntil: "2026-10-13 12:00", Unpaid: true, Status: order.FulfillmentPending}, "發票折讓等待買方同意，期限 2026-10-13 12:00。", "The invoice allowance awaits the buyer’s consent until 2026-10-13 12:00."},
		{"unpaid", OrderView{Unpaid: true, Status: order.FulfillmentPending}, "等待顧客付款，付款前無法備貨。", "Waiting for the customer to pay; picking cannot begin before payment."},
		{"paid pending", OrderView{Status: order.FulfillmentPending, Next: []Transition{{Value: order.FulfillmentPicking}}}, "已付款，可以開始備貨。", "Paid; picking can begin."},
		{"pending without a legal picking step", OrderView{Status: order.FulfillmentPending}, "這筆訂單沒有需要處理的事。", "This order needs no further action."},
		{"picking", OrderView{Status: order.FulfillmentPicking}, "備貨中，出貨時填寫物流與單號。", "Picking is in progress; enter the carrier and tracking number when dispatching."},
		{"shipped", OrderView{Status: order.FulfillmentShipped}, "已出貨，下一步是標記為已送達。", "Shipped; the next step is to mark it delivered."},
		{"delivered", OrderView{Status: order.FulfillmentDelivered}, "已送達，下一步是標記為已完成。", "Delivered; the next step is to mark it completed."},
		{"delivered with goods outstanding", OrderView{Status: order.FulfillmentDelivered, Shippable: []ShippableLine{{Remaining: 1}}}, "已送達部分包裹，還有商品待出貨。", "Some parcels have arrived; goods still need to be shipped."},
		{"completed", OrderView{Status: order.FulfillmentCompleted}, "這筆訂單沒有需要處理的事。", "This order needs no further action."},
		{"cancelled", OrderView{Status: order.FulfillmentCancelled}, "這筆訂單沒有需要處理的事。", "This order needs no further action."},
		{"cancelled and still owing", OrderView{Status: order.FulfillmentCancelled, Unpaid: true, OwedCents: 1000}, "這筆訂單沒有需要處理的事。", "This order needs no further action."},
		{"no status", OrderView{}, "這筆訂單沒有需要處理的事。", "This order needs no further action."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, locale := range i18n.Locales() {
				doc := orderStepsDocument(t, renderOrder(t, locale, &tc.view))
				summaries := orderStepsElements(doc, func(n *html.Node) bool { return attr(n, "id") == "order-next-step" })
				if len(summaries) != 1 {
					t.Errorf("%s: next-step summaries = %d, want one", locale, len(summaries))
					continue
				}
				want := tc.en
				if locale == i18n.ZhHant {
					want = tc.zh
				}
				if got := textOf(summaries[0]); got != want {
					t.Errorf("%s: summary = %q, want %q", locale, got, want)
				}
				parent := summaries[0].Parent
				if !hasElement(parent, func(n *html.Node) bool { return n.Data == "h1" }) {
					t.Errorf("%s: next-step summary is not beside the order heading", locale)
				}
			}
		})
	}
}

// TestOrderStatusStepForms: each order status step has its own POST form.
func TestOrderStatusStepForms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		next    []Transition
		primary order.FulfillmentStatus
	}{
		{"picking only", []Transition{{Value: order.FulfillmentPicking}}, order.FulfillmentPicking},
		{"cancel only", []Transition{{Value: order.FulfillmentCancelled}}, ""},
		{"picking and cancel", []Transition{{Value: order.FulfillmentPicking}, {Value: order.FulfillmentCancelled}}, order.FulfillmentPicking},
		{"cancel before picking", []Transition{{Value: order.FulfillmentCancelled}, {Value: order.FulfillmentPicking}}, order.FulfillmentPicking},
		{"deliver or complete", []Transition{{Value: order.FulfillmentDelivered}, {Value: order.FulfillmentCompleted}}, order.FulfillmentDelivered},
		{"keep projection order", []Transition{{Value: order.FulfillmentCompleted}, {Value: order.FulfillmentDelivered}}, order.FulfillmentCompleted},
		{"no moves", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, locale := range i18n.Locales() {
				page := renderOrder(t, locale, &OrderView{Number: "GO-1", Status: order.FulfillmentPending, Next: tc.next})
				doc := orderStepsDocument(t, page)
				forms := formsCarrying(doc, "status")
				if len(forms) != len(tc.next) {
					t.Errorf("%s: status forms = %d, want %d", locale, len(forms), len(tc.next))
					continue
				}
				seen := make(map[order.FulfillmentStatus]bool)
				primaryCount := 0
				for _, form := range forms {
					if attr(form, "method") != "post" || attr(form, "action") != "/admin/orders/GO-1/status" {
						t.Errorf("%s: status move is not its own native POST", locale)
					}
					inputs := orderStepsElements(form, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "status" })
					buttons := orderStepsElements(form, func(n *html.Node) bool { return n.Data == "button" && attr(n, "type") == "submit" })
					if len(inputs) != 1 || len(buttons) != 1 {
						t.Errorf("%s: status form has %d inputs and %d submits, want one each", locale, len(inputs), len(buttons))
						continue
					}
					input, button := inputs[0], buttons[0]
					status := order.FulfillmentStatus(attr(input, "value"))
					if attr(input, "type") != "hidden" || seen[status] {
						t.Errorf("%s: status %q is not one fixed move", locale, status)
					}
					seen[status] = true
					primary := strings.Contains(attr(button, "class"), "goen-btn--primary")
					inDetails := false
					for parent := form.Parent; parent != nil; parent = parent.Parent {
						if parent.Data == "form" {
							t.Error("status forms are nested")
						}
						if parent.Data == "details" {
							inDetails = true
							if _, open := attrPresent(parent, "open"); open {
								t.Error("other steps are open by default")
							}
							first := parent.FirstChild
							for first != nil && first.Type != html.ElementNode {
								first = first.NextSibling
							}
							if first == nil || first.Data != "summary" {
								t.Error("other steps have no leading summary")
							}
						}
					}
					if status == tc.primary {
						primaryCount++
						if !primary || inDetails || attr(button, "id") != "order-primary-step" {
							t.Errorf("%s: %s is not the primary move outside other steps", locale, status)
						}
					} else if primary || !inDetails {
						t.Errorf("%s: %s is not a secondary move in other steps", locale, status)
					}
					want := map[order.FulfillmentStatus]string{order.FulfillmentPicking: "Start picking", order.FulfillmentDelivered: "Mark delivered", order.FulfillmentCompleted: "Mark completed", order.FulfillmentCancelled: "Cancel order"}[status]
					if locale == i18n.ZhHant {
						want = map[order.FulfillmentStatus]string{order.FulfillmentPicking: "開始備貨", order.FulfillmentDelivered: "標記為已送達", order.FulfillmentCompleted: "標記為已完成", order.FulfillmentCancelled: "取消訂單"}[status]
					}
					if textOf(button) != want {
						t.Errorf("%s: %s submit label = %q, want %q", locale, status, textOf(button), want)
					}
				}
				if tc.primary == "" && primaryCount != 0 || tc.primary != "" && primaryCount != 1 {
					t.Errorf("%s: primary status moves = %d", locale, primaryCount)
				}
				for _, step := range tc.next {
					if !seen[step.Value] {
						t.Errorf("%s: lost legal step %s", locale, step.Value)
					}
				}
				summaries := orderStepsElements(doc, func(n *html.Node) bool {
					return n.Data == "summary" && (textOf(n) == "Other steps" || textOf(n) == "其他")
				})
				wantOther := len(tc.next)
				if tc.primary != "" {
					wantOther--
				}
				if wantOther > 0 {
					if len(summaries) != 1 {
						t.Errorf("%s: other-step disclosures = %d, want one", locale, len(summaries))
					}
				} else if len(summaries) != 0 {
					t.Errorf("%s: empty other-step disclosure remains", locale)
				}
				if len(orderStepsElements(doc, func(n *html.Node) bool { return n.Data == "select" && attr(n, "name") == "status" })) != 0 {
					t.Errorf("%s: the status select remains", locale)
				}
				if len(tc.next) > 0 {
					label, unpaid := "Steps you can take now", "An unpaid order cannot move into picking."
					if locale == i18n.ZhHant {
						label, unpaid = "可進行的下一步", "未付款的訂單無法進入備貨。"
					}
					if !strings.Contains(page, label) || !strings.Contains(page, unpaid) {
						t.Errorf("%s: the next-step hint or unpaid-order sentence is missing", locale)
					}
				}
			}
		})
	}
}

// TestOrderUnpaidWarning: the unpaid warning appears only for pending steps.
func TestOrderUnpaidWarning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status order.FulfillmentStatus
		next   order.FulfillmentStatus
		warn   bool
	}{
		{order.FulfillmentPending, order.FulfillmentPicking, true},
		{order.FulfillmentPicking, order.FulfillmentDelivered, false},
		{order.FulfillmentShipped, order.FulfillmentDelivered, false},
		{order.FulfillmentDelivered, order.FulfillmentCompleted, false},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			for _, locale := range i18n.Locales() {
				view := OrderView{Number: "GO-1", Status: tc.status, Next: []Transition{{Value: tc.next}}}
				doc := orderStepsDocument(t, renderOrder(t, locale, &view))
				warning := "An unpaid order cannot move into picking."
				if locale == i18n.ZhHant {
					warning = "未付款的訂單無法進入備貨。"
				}
				shown := hasElement(doc, func(n *html.Node) bool { return n.Data == "p" && textOf(n) == warning })
				if shown != tc.warn {
					t.Errorf("%s %s unpaid warning = %v, want %v", locale, tc.status, shown, tc.warn)
				}
			}
		})
	}
}

func orderStepsDocument(t *testing.T, page string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func orderStepsElements(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	if n.Type == html.ElementNode && match(n) {
		found = append(found, n)
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		found = append(found, orderStepsElements(child, match)...)
	}
	return found
}
