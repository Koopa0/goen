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
