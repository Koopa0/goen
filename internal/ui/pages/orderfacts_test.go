package pages

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// orderNow is noon on 9 October 2026 in Taipei; a parcel delivered on the 6th has 4 days of the right to cancel left.
var orderNow = time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)

// orderDay is a date column as the database returns it.
func orderDay(day int) time.Time { return time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC) }

func deliveredOn(day int, lines ...OrderLine) OrderShipment {
	return OrderShipment{
		Carrier: carrier.BlackCat, Tracking: "T" + strconv.Itoa(day),
		ShippedAt:      time.Date(2026, 10, day-1, 2, 0, 0, 0, time.UTC),
		DeliveredAt:    time.Date(2026, 10, day, 7, 20, 0, 0, time.UTC),
		RescissionEnds: orderDay(day + 7), GoodwillEnds: orderDay(day + 14),
		Lines: lines,
	}
}

func orderPage(t *testing.T, v *OrderView) string {
	t.Helper()
	v.Number, v.Now = "GO-261003-000014", orderNow
	if v.PlacedAt.IsZero() {
		v.PlacedAt = time.Date(2026, 10, 3, 6, 2, 0, 0, time.UTC)
	}
	return renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
}

var periodMarkup = regexp.MustCompile(`(?s)<div class="ui-period"[^>]*>.*?</div>`)

func periods(html string) []string { return periodMarkup.FindAllString(html, -1) }

func headphones() OrderLine {
	return OrderLine{SKU: "H1", Name: "耳機", UnitCents: 590000, Quantity: 1, WarrantyMonths: 12}
}

func TestOrderEventLabelsCoverEveryKind(t *testing.T) {
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

func TestTheAdminTimelineKeepsItsStatusWords(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	key, ok := OrderEvent{Kind: order.EventCancelled}.LookupLabelKey()
	if !ok || i18n.T(ctx, key) != "訂單取消" {
		t.Errorf("LookupLabelKey(cancelled) = %q, %v; want the status 訂單取消", i18n.T(ctx, key), ok)
	}
}

func TestTheHistoryReadsItsTimesInTheShortForm(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Timeline: []OrderEvent{{Kind: order.EventPlaced, At: time.Date(2026, 10, 3, 6, 2, 0, 0, time.UTC)}},
	})
	if want := `<time datetime="2026-10-03T14:02+08:00">10/3 14:02</time>`; !strings.Contains(html, want) {
		t.Errorf("the history does not contain %s", want)
	}
}

func TestEachParcelDrawsTheRightToCancelFromItsOwnDelivery(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentDelivered, ShowWarrantyLink: true,
		Lines:     []OrderLine{headphones(), headphones()},
		Shipments: []OrderShipment{deliveredOn(6, headphones()), deliveredOn(8, headphones())},
	})
	grids := periods(html)
	if len(grids) != 2 {
		t.Fatalf("an order of two delivered parcels draws %d grids, want 2", len(grids))
	}
	for i, want := range []string{"10/13", "10/15"} {
		if !strings.Contains(grids[i], "<small>"+want+"</small>") {
			t.Errorf("parcel %d's grid does not mark its own last day %s:\n%s", i+1, want, grids[i])
		}
	}
	if !strings.Contains(html, "包裹 1／2") || !strings.Contains(html, "包裹 2／2") {
		t.Error("the parcels are not told apart")
	}
}

var periodCell = regexp.MustCompile(`<i(?: [^>]*)?>`)

// The seven statutory days are the customer's right and the seven after them
// goen's offer, so the track marks exactly the offer's cells for the stylesheet
// to dash, whichever side today falls on.
func TestTheReturnWindowMarksOnlyTheGoodwillDays(t *testing.T) {
	t.Parallel()
	for _, delivered := range []int{6, 1} {
		html := orderPage(t, &OrderView{
			Status: order.FulfillmentDelivered, ShowWarrantyLink: true,
			Lines: []OrderLine{headphones()}, Shipments: []OrderShipment{deliveredOn(delivered, headphones())},
		})
		grids := periods(html)
		if len(grids) != 1 {
			t.Fatalf("delivered on the %d: %d grids, want the return window alone", delivered, len(grids))
		}
		cells := periodCell.FindAllString(grids[0], -1)
		if len(cells) != 14 {
			t.Fatalf("delivered on the %d: %d cells, want 14:\n%s", delivered, len(cells), grids[0])
		}
		for i, cell := range cells {
			if got, want := strings.Contains(cell, `data-span="extra"`), i >= 7; got != want {
				t.Errorf("delivered on the %d: day %d %s marked as goodwill = %v, want %v", delivered, i+1, cell, got, want)
			}
		}
	}
}

func TestAParcelNotYetDeliveredStatesTheRuleAndDrawsNoGrid(t *testing.T) {
	t.Parallel()
	waiting := OrderShipment{Carrier: carrier.BlackCat, Tracking: "T9", ShippedAt: orderNow.AddDate(0, 0, -1), Lines: []OrderLine{headphones()}}
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentShipped, ShowWarrantyLink: true,
		Lines: []OrderLine{headphones()}, Shipments: []OrderShipment{waiting},
	})
	if len(periods(html)) != 0 {
		t.Error("a parcel that has not arrived draws a grid")
	}
	if !strings.Contains(html, "猶豫期 7 天，收到次日起算。") {
		t.Error("an undelivered parcel does not state the rule")
	}
	if !strings.Contains(html, "送達後才能登錄") || strings.Contains(html, "/account/warranty/") {
		t.Error("a line in an undelivered parcel offers registration before delivery")
	}
}

func TestStorePickupCountsFromTheCollection(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentCompleted, Pickup: true,
		Lines: []OrderLine{headphones()}, Shipments: []OrderShipment{deliveredOn(6, headphones())},
	})
	for _, want := range []string{"<dt>取貨</dt>", "從取貨的次日起算", "10\u00a0月 6\u00a0日取貨", "取貨日起算"} {
		if !strings.Contains(html, want) {
			t.Errorf("a pickup order does not contain %q", want)
		}
	}
	if strings.Contains(html, "<dt>送達</dt>") {
		t.Error("a pickup order says it was delivered")
	}
}

func TestACancelledOrderHasNoRightToCancelAndNoReturn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		timeline   []OrderEvent
		wantRefund bool
	}{
		{"paid", []OrderEvent{
			{Kind: order.EventCancelled, At: orderNow}, {Kind: order.EventRefunded, At: orderNow},
		}, true},
		{"unpaid", []OrderEvent{{Kind: order.EventCancelled, At: orderNow}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			html := orderPage(t, &OrderView{
				Status: order.FulfillmentCancelled, SubtotalCents: 590000, Timeline: tt.timeline,
				Lines: []OrderLine{headphones()},
			})
			for _, banned := range []string{"申請退貨", "猶豫期", "剩餘", "退貨完成", "已全部退回", "個月"} {
				if strings.Contains(html, banned) {
					t.Errorf("a cancelled order still says %q", banned)
				}
			}
			if !strings.Contains(html, "<dt>取消</dt>") {
				t.Error("a cancelled order does not state when it was cancelled")
			}
			if got := strings.Contains(html, "<dt>退款</dt>"); got != tt.wantRefund {
				t.Errorf("a cancelled order states a refund = %v, want %v", got, tt.wantRefund)
			}
		})
	}
}

func TestAFullyReturnedOrderDrawsNoGrid(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentDelivered, SubtotalCents: 590000, ShowWarrantyLink: true,
		Lines:     []OrderLine{headphones()},
		Shipments: []OrderShipment{deliveredOn(6, headphones())},
		Returned:  &OrderReturned{At: time.Date(2026, 10, 7, 4, 0, 0, 0, time.UTC), RefundCents: 590000},
	})
	if len(periods(html)) != 0 {
		t.Error("a fully returned order draws a grid")
	}
	// The panel states the money only: the goods of an approved return may still be on their way back.
	for _, banned := range []string{"剩餘", "申請退貨", "最後一天", "已送達", "付款完成", "退貨完成", "已全部退回"} {
		if strings.Contains(html, banned) {
			t.Errorf("a fully returned order still says %q", banned)
		}
	}
	if !strings.Contains(html, `goen-pagehead__eyebrow">已退款<`) {
		t.Error("a fully returned order's eyebrow does not say it was refunded")
	}
	for _, want := range []string{"<dt>退款日期</dt>", "<dt>退款</dt>", "NT$5,900", "這筆訂單已全部退款。"} {
		if !strings.Contains(html, want) {
			t.Errorf("a fully returned order does not state %q", want)
		}
	}
}

func TestAGuestSeesTheWarrantyMonthsAndNoRegisterLink(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentDelivered, ShowWarrantyLink: false,
		Lines: []OrderLine{headphones()}, Shipments: []OrderShipment{deliveredOn(6, headphones())},
	})
	if strings.Contains(html, "/account/warranty/") {
		t.Error("a guest is offered the registration link")
	}
	for _, want := range []string{"12", "個月", "登錄保固需要帳號"} {
		if !strings.Contains(html, want) {
			t.Errorf("a guest's warranty does not say %q", want)
		}
	}
}

// A registered warranty states the day its cover ends and draws nothing: a
// grid of its months would fill one cell and say only "the first month".
func TestARegisteredWarrantyStatesItsEndInWords(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		months int
		until  time.Time
		want   string
	}{
		{"a year", 12, time.Date(2027, 10, 6, 0, 0, 0, 0, time.UTC), `<time datetime="2027-10-06">`},
		{"ten years", 120, time.Date(2036, 10, 6, 0, 0, 0, 0, time.UTC), `<time datetime="2036-10-06">`},
	} {
		registered := headphones()
		registered.WarrantyMonths, registered.Registered, registered.WarrantyUntil = tt.months, 1, tt.until
		html := orderPage(t, &OrderView{
			Status: order.FulfillmentDelivered, ShowWarrantyLink: true,
			Lines: []OrderLine{registered}, Shipments: []OrderShipment{deliveredOn(6, registered)},
		})
		for _, want := range []string{"<dt>保固至</dt>", tt.want} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: a registered warranty does not state %s", tt.name, want)
			}
		}
		if strings.Contains(html, "/account/warranty/") {
			t.Errorf("%s: a fully registered line offers registration again", tt.name)
		}
		i := strings.Index(html, `class="goen-order__lines"`)
		if i < 0 {
			t.Fatalf("%s: no order lines", tt.name)
		}
		if strings.Contains(html[i:], "ui-period") {
			t.Errorf("%s: a line draws its warranty; the end date says it", tt.name)
		}
		if got := len(periods(html)); got != 1 {
			t.Errorf("%s: %d periods on the page, want only the right to cancel", tt.name, got)
		}
	}
}

func TestTheHeadFactsOfAnOrderWithOneDeliveredParcel(t *testing.T) {
	t.Parallel()
	html := orderPage(t, &OrderView{
		Status: order.FulfillmentDelivered, SubtotalCents: 3980000,
		Lines: []OrderLine{headphones()}, Shipments: []OrderShipment{deliveredOn(6, headphones())},
	})
	for _, want := range []string{"<dt>送出</dt>", "<dt>總計</dt>", "<dt>送達</dt>", `datetime="2026-10-06"`, "15:20，黑貓宅急便", "NT$39,800"} {
		if !strings.Contains(html, want) {
			t.Errorf("the head facts do not contain %q", want)
		}
	}
}

func TestDaysLeftIsABareFigureInEnglish(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		day  int
		want string
	}{
		{"one day", 12, "<dt>Days left</dt><dd>1</dd>"},
		{"two days", 11, "<dt>Days left</dt><dd>2</dd>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &OrderView{
				Number: "GO-1", Status: order.FulfillmentDelivered, Now: time.Date(2026, 10, tt.day, 4, 0, 0, 0, time.UTC),
				PlacedAt:  orderNow,
				Lines:     []OrderLine{headphones()},
				Shipments: []OrderShipment{deliveredOn(6, headphones())},
			}
			html := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Order(layouts.Page{Title: "Order"}, v))
			if !strings.Contains(html, tt.want) {
				t.Errorf("days left do not read %q", tt.want)
			}
		})
	}
}
