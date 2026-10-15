package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/warranty"
)

func TestWarrantyTermsStateThePurchasedDuration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale       i18n.Locale
		months       int
		want         string
		registration string
	}{
		{locale: i18n.ZhHant, months: 12, want: "12 個月", registration: "1 年"},
		{locale: i18n.ZhHant, months: 1, want: "1 個月", registration: "1 個月"},
		{locale: i18n.En, months: 12, want: "12 months", registration: "1-year"},
		{locale: i18n.En, months: 24, want: "24 months", registration: "2-year"},
		{locale: i18n.En, months: 1, want: "1 month", registration: "1-month"},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		if got := (&CompareProduct{WarrantyMonths: tc.months}).Warranty(ctx); got != tc.want {
			t.Errorf("%v: compare page, %d months = %q, want %q", tc.locale, tc.months, got, tc.want)
		}
		if got := (warranty.WarrantyLine{HasTerm: true, Months: tc.months}).TermText(ctx); got != tc.registration {
			t.Errorf("%v: warranty page, %d months = %q, want %q", tc.locale, tc.months, got, tc.registration)
		}
	}
}

func TestTheOrderHistoryHidesAShippedEventsNote(t *testing.T) {
	t.Parallel()
	page := renderToString(t, Order(layouts.Page{Title: "o"}, &OrderView{
		Status: order.FulfillmentCancelled,
		Timeline: []OrderEvent{
			{Kind: order.EventShipped, Note: "shipped-note-text"},
			{Kind: order.EventCancelled, Note: "cancelled-note-text"},
		},
	}))
	if strings.Contains(page, "shipped-note-text") {
		t.Error("the order history prints the shipped event's note")
	}
	if !strings.Contains(page, "cancelled-note-text") {
		t.Error("the order history dropped another event's note")
	}
}

func TestThePointsRuleNamesTheStepInPoints(t *testing.T) {
	t.Parallel()
	for loc, want := range map[i18n.Locale]string{
		i18n.ZhHant: "最少 20 點，每次以 10 點為單位兌換",
		i18n.En:     "At least 20 points, in whole multiples of 10 points.",
	} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Points(layouts.Page{Title: "p"}, PointsView{PerCredit: 10, Minimum: 20, Redeemable: 40}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), want) {
			t.Errorf("%v: the points rule does not read %q", loc, want)
		}
	}
}
