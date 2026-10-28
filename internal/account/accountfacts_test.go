package account

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestOrderLastDay: an order row in the account list shows a last day
// only when one day holds for the whole order.
func TestOrderLastDay(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	placed := shoptime.Date{Year: 2026, Month: time.October, Day: 1}
	lastDay := shoptime.Date{Year: 2026, Month: time.October, Day: 13}
	for _, tc := range []struct {
		name      string
		delivered bool
		want      bool
	}{
		{"parcels without one day", false, false},
		{"one day", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := &View{Orders: []Order{{
				Number: "GO-261001-000001", Status: order.FulfillmentShipped, PlacedAt: placed, TotalCents: 106000,
				LineCount: 1, OneLastDay: tc.delivered, LastDay: lastDay,
			}}}
			html := renderToString(t, Account(Meta(ctx), view))
			if !strings.Contains(html, `class="ui-statline ui-statline--s"`) {
				t.Error("an order row carries no small stat line")
			}
			if !strings.Contains(html, `datetime="2026-10-01"`) {
				t.Error("an order row does not state its placed day")
			}
			if got := strings.Contains(html, `datetime="2026-10-13"`); got != tc.want {
				t.Errorf("Account(order oneLastDay=%v) shows the last day = %v, want %v", tc.delivered, got, tc.want)
			}
			if got := strings.Contains(html, "猶豫期到"); got != tc.want {
				t.Errorf("Account(order oneLastDay=%v) shows the rescission label = %v, want %v", tc.delivered, got, tc.want)
			}
			if strings.Contains(html, "最後一天") {
				t.Errorf("Account(order oneLastDay=%v) labels a day 最後一天, which is the last day of nothing", tc.delivered)
			}
		})
	}
}
