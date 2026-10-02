package email

import (
	"strings"
	"testing"
)

func TestAPickupDispatchNoticeSaysStorePickup(t *testing.T) {
	t.Parallel()
	for locale, store := range map[string]string{"en": "convenience store", "zh-TW": "超商"} {
		n, sink := notifier(t)
		send := func(pickup bool) string {
			if err := n.SendOrderShipped(t.Context(), &OrderShipped{
				Locale: locale, Email: "a@b.co", Name: "Alex", OrderNumber: "GO-1",
				Carrier: "C", Tracking: "T", Pickup: pickup,
			}); err != nil {
				t.Fatal(err)
			}
			return sink.msg.Body
		}
		if body := send(true); !strings.Contains(body, store) {
			t.Errorf("%s pickup notice does not say store pickup: %s", locale, body)
		}
		if body := send(false); strings.Contains(body, store) {
			t.Errorf("%s home notice mentions the store: %s", locale, body)
		}
	}
}

func TestADispatchNoticeLinksTheCarriersTrackingPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, carrier, want, wantName string
	}{
		{"black cat opens the parcel", "black_cat", "https://www.t-cat.com.tw/Inquire/TraceDetail.aspx?BillID=TW123", "黑貓宅急便"},
		{"hct gives its lookup page", "hct", "https://www.hct.com.tw/Search/SearchGoods_n.aspx", "新竹物流"},
		{"a carrier with no confirmed page links nothing", "ok_mart", "", "OK 超商"},
		{"a value outside the closed set links nothing", "Black Cat", "", "Black Cat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n, sink := notifier(t)
			if err := n.SendOrderShipped(t.Context(), &OrderShipped{
				Locale: "zh-TW", Email: "a@b.co", Name: "Alex", OrderNumber: "GO-1",
				Carrier: tt.carrier, Tracking: "TW123",
			}); err != nil {
				t.Fatal(err)
			}
			body := sink.msg.Body
			if !strings.Contains(body, tt.wantName) {
				t.Errorf("body does not name the carrier %q:\n%s", tt.wantName, body)
			}
			if tt.want == "" {
				if strings.Contains(body, "查詢物流") {
					t.Errorf("body links a tracking page for %q:\n%s", tt.carrier, body)
				}
				return
			}
			if !strings.Contains(body, tt.want) {
				t.Errorf("body does not link %q:\n%s", tt.want, body)
			}
		})
	}
}
