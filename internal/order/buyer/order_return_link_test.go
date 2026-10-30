package buyer

import (
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/frame"
)

// returnLinksOf is the text of every link in the action column that goes to the order's return page.
func returnLinksOf(t *testing.T, html, number string) []string {
	t.Helper()
	var texts []string
	var walk func(n *nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.ElementNode && n.Data == "a" && attrValue(n, "href") == "/orders/"+number+"/return" {
			texts = append(texts, strings.TrimSpace(nodeText(n)))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(actionsOf(t, html))
	return texts
}

// returnOrder is a paid order with one parcel on its way (or, delivered, received), the lines of which can be returned.
func returnOrder(status order.FulfillmentStatus, delivered bool) *OrderView {
	line := OrderLine{SKU: "S1", Name: "耳機", UnitCents: 100000, Quantity: 1}
	parcel := OrderShipment{
		Carrier: shipping.CarrierBlackCat, Tracking: "T1", ShippedAt: orderNow.AddDate(0, 0, -2),
		RescissionDays: 7, Lines: []OrderLine{line},
	}
	if delivered {
		parcel.DeliveredAt, parcel.RescissionEnds, parcel.GoodwillEnds = orderNow.AddDate(0, 0, -1), orderDay(13), orderDay(20)
	}
	return &OrderView{
		Number: "GO-261009-000031", Status: status, Committed: true, Now: orderNow,
		SubtotalCents: 100000, ShippingName: "宅配", Lines: []OrderLine{line}, Shipments: []OrderShipment{parcel},
	}
}

// TestOrderReturnLink: the order page offers 申請退貨 from shipment and says, until a parcel
// has arrived, that the days count from receipt. With a request nobody has decided it offers
// 查看退貨申請 under a line saying so, not a request that fails on a second press. Where a
// request exists but nothing is left to request, the link still leads to it.
func TestOrderReturnLink(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		view         func() *OrderView
		wantLinks    []i18n.Key
		wantAfter    bool
		wantOpenLine bool
	}{
		{"shipped", func() *OrderView { return returnOrder(order.FulfillmentShipped, false) }, []i18n.Key{i18n.KeyOrderReturn}, true, false},
		{"delivered", func() *OrderView { return returnOrder(order.FulfillmentDelivered, true) }, []i18n.Key{i18n.KeyOrderReturn}, false, false},
		{"open request", func() *OrderView {
			v := returnOrder(order.FulfillmentShipped, false)
			v.OpenReturn, v.HasReturn = true, true
			return v
		}, []i18n.Key{i18n.KeyOrderReturnView}, false, true},
		{"decided request, more to return", func() *OrderView {
			v := returnOrder(order.FulfillmentDelivered, true)
			v.HasReturn = true
			return v
		}, []i18n.Key{i18n.KeyOrderReturn}, false, false},
		{"refunded in full", func() *OrderView {
			v := returnOrder(order.FulfillmentDelivered, true)
			v.HasReturn, v.Returned = true, &OrderReturned{At: orderNow, RefundCents: 100000}
			return v
		}, []i18n.Key{i18n.KeyOrderReturnView}, false, false},
		{"no request, not shipped", func() *OrderView {
			v := returnOrder(order.FulfillmentPicking, false)
			v.Shipments = nil
			return v
		}, nil, false, false},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tt.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				v := tt.view()
				html := renderComponent(t, ctx, Order(frame.Page{Title: "order"}, v))

				want := make([]string, 0, len(tt.wantLinks))
				for _, key := range tt.wantLinks {
					want = append(want, i18n.T(ctx, key))
				}
				got := returnLinksOf(t, html, v.Number)
				if len(got) != len(want) || (len(want) == 1 && got[0] != want[0]) {
					t.Errorf("the links to the return page read %q, want %q", got, want)
				}

				after := i18n.Count(ctx, i18n.KeyOrderReturnAfterDelivery, 7, 7)
				if gotAfter := strings.Contains(nodeText(actionsOf(t, html)), after); gotAfter != tt.wantAfter {
					t.Errorf("the days-from-receipt line %q renders = %v, want %v", after, gotAfter, tt.wantAfter)
				}
				open := i18n.T(ctx, i18n.KeyOrderReturnOpen)
				if gotOpen := strings.Contains(html, `id="order-return-open"`) && strings.Contains(html, open); gotOpen != tt.wantOpenLine {
					t.Errorf("the open-request line %q renders = %v, want %v", open, gotOpen, tt.wantOpenLine)
				}
			})
		}
	}
}

// TestOrderView_CanRequestReturn: a request nobody has decided refuses another, so the page does
// not offer one; a decided request does not.
func TestOrderView_CanRequestReturn(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		view OrderView
		want bool
	}{
		{"shipped", OrderView{Status: order.FulfillmentShipped}, true},
		{"picking", OrderView{Status: order.FulfillmentPicking}, false},
		{"open request", OrderView{Status: order.FulfillmentDelivered, OpenReturn: true, HasReturn: true}, false},
		{"decided request", OrderView{Status: order.FulfillmentDelivered, HasReturn: true}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.view.CanRequestReturn(); got != tt.want {
				t.Errorf("CanRequestReturn() = %v, want %v", got, tt.want)
			}
		})
	}
}
