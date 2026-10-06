package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAccountOrderRowLastDayOnlyOnceDelivered(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	placed := shoptime.Date{Year: 2026, Month: time.October, Day: 1}
	lastDay := shoptime.Date{Year: 2026, Month: time.October, Day: 13}
	for _, tc := range []struct {
		name      string
		delivered bool
		want      bool
	}{
		{"on its way", false, false},
		{"delivered", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := &AccountView{Orders: []AccountOrder{{
				Number: "GO-261001-000001", Status: order.FulfillmentShipped, PlacedAt: placed, TotalCents: 106000,
				LineCount: 1, Delivered: tc.delivered, LastDay: lastDay,
			}}}
			html := renderToString(t, Account(AccountMeta(ctx), view))
			if !strings.Contains(html, `class="ui-statline ui-statline--s"`) {
				t.Error("an order row carries no small stat line")
			}
			if !strings.Contains(html, `datetime="2026-10-01"`) {
				t.Error("an order row does not state its placed day")
			}
			if got := strings.Contains(html, `datetime="2026-10-13"`); got != tc.want {
				t.Errorf("Account(order delivered=%v) shows the last day = %v, want %v", tc.delivered, got, tc.want)
			}
			if got := strings.Contains(html, i18n.T(ctx, i18n.KeyOrderLastDay)); got != tc.want {
				t.Errorf("Account(order delivered=%v) shows the last-day label = %v, want %v", tc.delivered, got, tc.want)
			}
		})
	}
}

func TestWarrantyRowListsCoverEnd(t *testing.T) {
	t.Parallel()
	view := WarrantyListView{Rows: []Warranty{{
		Name: "Buds", Order: "GO-261001-000001", InForce: true,
		ExpiresOn: shoptime.Date{Year: 2027, Month: time.October, Day: 6},
	}}}
	html := renderToString(t, WarrantyList(layouts.Page{Title: "Warranty"}, view))
	for _, want := range []string{"ui-statline--s", "保固至", `datetime="2027-10-06"`, "goen-warranty__item"} {
		if !strings.Contains(html, want) {
			t.Errorf("WarrantyList row lacks %q", want)
		}
	}
}
