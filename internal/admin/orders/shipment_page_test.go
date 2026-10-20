package orders

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestLaterParcelArrivalActionBelongsToItsUndeliveredParcel(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, status := range []order.FulfillmentStatus{order.FulfillmentPending, order.FulfillmentPicking, order.FulfillmentShipped, order.FulfillmentDelivered, order.FulfillmentCompleted, order.FulfillmentCancelled} {
			t.Run(locale.Tag()+"/"+string(status), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := OrderView{
					Number: "GO-261012-000001", Status: status,
					Shipments: []Shipment{
						{ID: "11111111-1111-4111-8111-111111111111", Carrier: shipping.CarrierBlackCat, Tracking: "FIRST", ShippedAt: "2026-10-10 09:00", DeliveredAt: "2026-10-11 09:00"},
						{ID: "22222222-2222-4222-8222-222222222222", Carrier: shipping.CarrierBlackCat, Tracking: "SECOND", ShippedAt: "2026-10-12 09:00"},
					},
				}
				page := renderComponent(t, ctx, Order(layouts.Page{}, &view))
				label := "Mark delivered"
				if locale == i18n.ZhHant {
					label = "標記已送達"
				}
				want := 0
				if status == order.FulfillmentDelivered {
					want = 1
				}
				if got := strings.Count(page, label); got != want {
					t.Errorf("%s order arrival actions = %d, want %d", status, got, want)
				}
				action := `/admin/orders/GO-261012-000001/shipments/22222222-2222-4222-8222-222222222222/delivered`
				if got := strings.Contains(page, `method="post" action="`+action+`"`); got != (want == 1) {
					t.Errorf("second parcel POST form = %t, want %t", got, want == 1)
				}
				if want == 1 && !strings.Contains(page, `<span class="goen-sr-only"> SECOND</span>`) {
					t.Error("the arrival button does not name its own parcel for assistive technology")
				}
				if strings.Contains(page, `/shipments/11111111-1111-4111-8111-111111111111/delivered`) {
					t.Error("the already delivered parcel offers an arrival action")
				}
			})
		}
	}
}

func TestParcelArrivalAuditNamesTheActionInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale                  i18n.Locale
		want, entity, dateLabel string
	}{
		{locale: i18n.ZhHant, want: "標記包裹已送達", entity: "包裹", dateLabel: "送達時間"},
		{locale: i18n.En, want: "Mark parcel delivered", entity: "Parcel", dateLabel: "Delivered at"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			entry := audit.Entry{Action: "shipment.delivered", Entity: "order_shipments",
				Changes: []audit.Change{{Field: "delivered_at", After: "2026-10-12 10:00"}}}
			if got := entry.Label(ctx); got != tt.want {
				t.Errorf("arrival audit label = %q, want %q", got, tt.want)
			}
			if got := entry.EntityLabel(ctx); got != tt.entity {
				t.Errorf("arrival audit entity = %q, want %q", got, tt.entity)
			}
			page := renderComponent(t, ctx, audit.Audit(layouts.Page{}, audit.View{Rows: []audit.Entry{entry}}))
			if !strings.Contains(page, "<dt>"+tt.dateLabel+"</dt>") || !strings.Contains(page, "<dd>2026-10-12 10:00</dd>") {
				t.Error("arrival audit does not show its labelled delivery time")
			}
		})
	}
}
