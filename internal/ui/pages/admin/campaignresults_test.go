package admin

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// results is days days before a campaign and the first days of it, from 2026-09-22,
// with the units each day sold in order.
func results(days int, units ...int64) *CampaignResults {
	buckets := make([]chart.Bucket, 0, len(units))
	for i, u := range units {
		buckets = append(buckets, chart.Bucket{Day: time.Date(2026, 9, 22+i, 0, 0, 0, 0, time.UTC), Value: u})
	}
	start := time.Date(2026, 9, 22+days, 0, 0, 0, 0, time.UTC)
	return &CampaignResults{
		Units: chart.Series{Buckets: buckets, Partial: true}, Days: days, Products: 6, Cut: "15:20",
		Campaign: chart.Span{From: start, To: start.AddDate(0, 0, 13), Label: "Autumn picks"},
	}
}

func renderResults(t *testing.T, locale i18n.Locale, v CampaignView) string {
	t.Helper()
	var b bytes.Buffer
	if err := CampaignForm(layouts.Page{Title: "c"}, v).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("CampaignForm.Render: %v", err)
	}
	return b.String()
}

func TestCampaignResultsAreDrawnFromThreeDaysAndTenUnitsBeforeAndDuring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		r            *CampaignResults
		reached, got bool
	}{
		{"day 2, 1 unit", results(2, 0, 0, 1, 0), false, false},
		{"3 days, 9 units", results(3, 1, 1, 1, 2, 2, 2), false, false},
		{"2 days, 12 units", results(2, 3, 3, 3, 3), false, false},
		{"3 days, 10 units", results(3, 1, 1, 1, 2, 2, 3), true, true},
		{"10 units on two days", results(5, 0, 0, 0, 0, 0, 0, 0, 0, 5, 5), true, false},
	}
	for _, tt := range tests {
		if got := tt.r.Reached(); got != tt.reached {
			t.Errorf("%s: Reached() = %v, want %v", tt.name, got, tt.reached)
		}
		if got := tt.r.Drawn(); got != tt.got {
			t.Errorf("%s: Drawn() = %v, want %v", tt.name, got, tt.got)
		}
	}
}

func TestCampaignResultsSentencesCountBeforeAndDuringEachInItsOwnPlural(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	r := results(2, 0, 0, 1, 0)
	if got, want := r.Facts(ctx), "0 units sold in the campaign's first 2 days; 1 unit in the 2 days before."; got == want {
		t.Fatalf("Facts() = %q: the campaign's units and the days before have swapped", got)
	}
	if got, want := r.Facts(ctx), "1 unit sold in the campaign's first 2 days; 0 units in the 2 days before."; got != want {
		t.Errorf("Facts() = %q, want %q", got, want)
	}
	if got, want := r.Hint(ctx), "The daily columns appear once the campaign has run 3 days and 10 units have sold, before and during."; got != want {
		t.Errorf("Hint() = %q, want %q", got, want)
	}
	if got := results(3, 1, 1, 1, 2, 2, 3).Hint(ctx); got != "" {
		t.Errorf("Hint() = %q once the columns are drawn, want none", got)
	}
	zh := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if got, want := r.Facts(zh), "活動開始後的 2 天 售出 1 件；開始前的 2 天 售出 0 件。"; strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(want, " ", "") {
		t.Errorf("Facts() in Chinese = %q, want %q", got, want)
	}
}

func TestCampaignPageDrawsTheDaysOrSaysThemInASentence(t *testing.T) {
	t.Parallel()

	drawn := renderResults(t, i18n.En, CampaignView{Slug: "autumn", Results: results(3, 1, 1, 1, 2, 2, 3)})
	for _, want := range []string{
		"<svg", "goen-chart__hue--previous", "2 units sold in the campaign",
		`<td class="goen-chart__spans">Before</td>`, "Counts the campaign&rsquo;s current 6 products",
	} {
		if !strings.Contains(drawn, want) {
			t.Errorf("a campaign with 10 units over 3 days lacks %q", want)
		}
	}
	for name, r := range map[string]*CampaignResults{
		"day 2":                results(2, 0, 0, 1, 0),
		"10 units on two days": results(5, 0, 0, 0, 0, 0, 0, 0, 0, 5, 5),
	} {
		page := renderResults(t, i18n.En, CampaignView{Slug: "autumn", Results: r})
		if strings.Contains(page, "goen-chart__plot") || !strings.Contains(page, "sold in the campaign&rsquo;s first") {
			t.Errorf("%s: the page draws columns it should leave to a sentence, or lacks the sentence", name)
		}
		if got, want := strings.Contains(page, "The daily columns appear once"), !r.Reached(); got != want {
			t.Errorf("%s: says what the columns wait for = %v, want %v", name, got, want)
		}
	}
	if page := renderResults(t, i18n.En, CampaignView{Slug: "autumn"}); strings.Contains(page, "Results") {
		t.Error("a campaign with no results shows a Results section")
	}
	if page := renderResults(t, i18n.En, CampaignView{Slug: "autumn", ResultsUnavailable: true}); !strings.Contains(page, "unavailable right now") {
		t.Error("a campaign whose results could not be read says nothing")
	}
}
