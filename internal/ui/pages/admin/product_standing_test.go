package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
)

// thirteenWeeks is 91 shop days ending Oct 6, with units on the days given by
// their offset from Jul 8.
func thirteenWeeks(units map[int]int64) ProductSales {
	first := time.Date(2026, time.July, 8, 0, 0, 0, 0, time.UTC)
	days := chart.Series{Partial: true}
	for i := range 91 {
		days.Buckets = append(days.Buckets, chart.Bucket{Day: first.AddDate(0, 0, i), Value: units[i]})
	}
	return ProductSales{Days: days, Cut: "15:20"}
}

func renderStanding(t *testing.T, loc i18n.Locale, v ProductView) string {
	t.Helper()
	return renderComponent(t, i18n.WithLocale(t.Context(), loc), productStanding(v))
}

func TestWeeklyUnitsAreToldInASentenceUntilThreeDaysSold(t *testing.T) {
	tests := []struct {
		name  string
		units map[int]int64
		want  string
	}{
		{"none", nil, "Nothing sold Jul 8–Oct 6."},
		{"one day", map[int]int64{90: 2}, "Jul 8–Oct 6 had sales on 1 day only: 2 units on Oct 6."},
		{"two days", map[int]int64{80: 1, 90: 2}, "Jul 8–Oct 6 had sales on 2 days only: 1 unit on Sep 26, 2 units on Oct 6."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderStanding(t, i18n.En, ProductView{Sales: thirteenWeeks(tt.units)})
			if !strings.Contains(got, tt.want) {
				t.Errorf("sentence missing %q in\n%s", tt.want, got)
			}
			if strings.Contains(got, "goen-chart__plot") {
				t.Error("drew columns for fewer than three days with sales")
			}
		})
	}
}

func TestWeeklyUnitsAreThirteenWholeWeeks(t *testing.T) {
	sparse := map[int]int64{10: 1, 30: 2, 50: 1, 90: 3}
	got := renderStanding(t, i18n.En, ProductView{Sales: thirteenWeeks(sparse)})
	if !strings.Contains(got, "Units sold on 4 days of this period.") {
		t.Errorf("sparse sentence missing in\n%s", got)
	}
	_, table, _ := strings.Cut(got, `<table class="goen-chart__table"`)
	table, _, _ = strings.Cut(table, "</table>")
	if rows := strings.Count(table, `<th scope="row">`); rows != 13 {
		t.Errorf("table rows = %d, want 13 weeks", rows)
	}
	if strings.Contains(got, "earliest stretch") {
		t.Error("the earliest week is short: the 91 days are not whole weeks")
	}
	if !strings.Contains(got, "up to 15:20") {
		t.Error("the last week is not said to be counted up to the time of day")
	}
}

func TestWeeklyUnitsNameTheBestWeek(t *testing.T) {
	// Jul 8 is day 0, so week 5 starts on day 35, Aug 12.
	units := map[int]int64{0: 1, 10: 1, 20: 1, 36: 4, 37: 3, 70: 2, 90: 1}
	got := renderStanding(t, i18n.En, ProductView{Sales: thirteenWeeks(units)})
	if want := "The best week was the 7 days from Aug 12, with 7 units."; !strings.Contains(got, want) {
		t.Errorf("missing %q in\n%s", want, got)
	}

	tied := map[int]int64{0: 1, 10: 1, 20: 5, 36: 5, 45: 1, 70: 2, 90: 1}
	got = renderStanding(t, i18n.En, ProductView{Sales: thirteenWeeks(tied)})
	if want := "2 weeks tied for the most, 5 units each."; !strings.Contains(got, want) {
		t.Errorf("missing %q in\n%s", want, got)
	}
}

func TestAWeeklyChartThatCouldNotBeReadIsNotAnEmptyOne(t *testing.T) {
	got := renderStanding(t, i18n.En, ProductView{Sales: ProductSales{Unavailable: true}, Ratings: ProductRatings{Unavailable: true}})
	if n := strings.Count(got, `role="status"`); n != 2 {
		t.Errorf("status lines = %d, want 2", n)
	}
	if strings.Contains(got, "Nothing sold") || strings.Contains(got, "No reviews yet") {
		t.Error("an unreadable figure was told as an empty one")
	}
}

func TestTheRatingSpreadNeedsFiveReviews(t *testing.T) {
	tests := []struct {
		name    string
		ratings ProductRatings
		loc     i18n.Locale
		want    string
		spread  bool
	}{
		{"none", ProductRatings{}, i18n.En, "No reviews yet.", false},
		{"two", ProductRatings{Count: 2, Average: 4.5, Stars: [5]int64{1, 1}}, i18n.ZhHant, "2 則評價：5★ 1、4★ 1。", false},
		{"four", ProductRatings{Count: 4, Average: 4, Stars: [5]int64{2, 1, 0, 0, 1}}, i18n.En, "4 reviews: 5★ 2, 4★ 1, 1★ 1.", false},
		{"five", ProductRatings{Count: 5, Average: 4.4, Stars: [5]int64{3, 1, 0, 1, 0}}, i18n.En, "5 reviews, average 4.4 out of 5.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderStanding(t, tt.loc, ProductView{Ratings: tt.ratings})
			if !strings.Contains(got, tt.want) {
				t.Errorf("missing %q in\n%s", tt.want, got)
			}
			if drawn := strings.Contains(got, `class="goen-spread"`); drawn != tt.spread {
				t.Errorf("spread drawn = %v, want %v", drawn, tt.spread)
			}
		})
	}
}

func TestTheRatingSpreadCountsEveryStarInTextAndScalesToTheLargest(t *testing.T) {
	got := renderStanding(t, i18n.En, ProductView{Ratings: ProductRatings{Count: 12, Average: 4.2, Stars: [5]int64{8, 2, 1, 0, 1}}})
	_, table, _ := strings.Cut(got, `class="goen-spread"`)
	for _, want := range []string{
		`<th scope="row">5 stars</th>`, `<th scope="row">1 star</th>`,
		`<span class="goen-sr-only">8</span>`, `<span class="goen-sr-only">0</span>`,
		`width="100.00%"`, `width="25.00%"`,
	} {
		if !strings.Contains(table, want) {
			t.Errorf("spread lacks %q in\n%s", want, table)
		}
	}
	if strings.Contains(table, "%</td>") || strings.Contains(table, "%</span>") {
		t.Error("the spread states a percentage; counts only")
	}
}
