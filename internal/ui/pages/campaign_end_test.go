package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestACampaignNamesItsLastDayOnlyWithinThirtyDays(t *testing.T) {
	t.Parallel()

	// 12:00 in Taipei.
	now := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	december := time.Date(2026, 12, 20, 4, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		now    time.Time
		endsAt time.Time
		want   string
	}{
		{"29 days", i18n.ZhHant, now, now.AddDate(0, 0, 29), "10 月 31 日"},
		{"30 days", i18n.ZhHant, now, now.AddDate(0, 0, 30), "11 月 1 日"},
		{"31 days", i18n.ZhHant, now, now.AddDate(0, 0, 31), ""},
		{"29 days in English", i18n.En, now, now.AddDate(0, 0, 29), "Oct 31"},
		{"31 days in English", i18n.En, now, now.AddDate(0, 0, 31), ""},
		{"a year", i18n.ZhHant, now, now.AddDate(1, 0, 0), ""},
		{"past any duration", i18n.ZhHant, now, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ""},
		{"into next year", i18n.ZhHant, december, time.Date(2027, 1, 5, 4, 0, 0, 0, time.UTC), "2027 年 1 月 5 日"},
		{"into next year in English", i18n.En, december, time.Date(2027, 1, 5, 4, 0, 0, 0, time.UTC), "Jan 5, 2027"},
	} {
		got := CampaignEndsOn(i18n.WithLocale(t.Context(), tt.locale), tt.endsAt, tt.now)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestACampaignWithNoNearEndSaysNoDate(t *testing.T) {
	t.Parallel()

	far := renderIn(t, i18n.ZhHant, Campaign(layouts.Page{Title: "c"}, CampaignView{Slug: "c", Title: "秋日選物"}))
	if strings.Contains(far, "goen-pagehead__sub") || strings.Contains(far, "活動至") {
		t.Error("a campaign whose end is not near still prints an end line")
	}
	near := renderIn(t, i18n.ZhHant, Campaign(layouts.Page{Title: "c"}, CampaignView{Slug: "c", Title: "秋日選物", EndsOn: "10 月 31 日"}))
	if !strings.Contains(near, "活動至 10 月 31 日") {
		t.Error("a campaign ending soon does not say its last day")
	}

	strip := renderIn(t, i18n.ZhHant, campaignStrip([]CampaignSummary{
		{Slug: "far", Title: "秋日選物", Products: 6},
		{Slug: "near", Title: "茶與咖啡週", Products: 4, EndsOn: "10 月 31 日"},
	}))
	if !strings.Contains(strip, "至 10 月 31 日") {
		t.Error("the offers strip does not name a near last day")
	}
	if strings.Count(strip, "·") != 1 {
		t.Errorf("the offers strip separates %d facts, want only the near campaign's", strings.Count(strip, "·"))
	}
}
