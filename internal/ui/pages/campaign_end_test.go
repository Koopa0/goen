package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

func TestACampaignNamesItsLastDayHoweverFarOff(t *testing.T) {
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
		{"midnight end", i18n.ZhHant, now, time.Date(2026, 10, 30, 16, 0, 0, 0, time.UTC), "10\u00a0月 30\u00a0日"},
		{"midnight end into a month", i18n.ZhHant, now, time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), "10\u00a0月 31\u00a0日"},
		{"23:59 end", i18n.ZhHant, now, time.Date(2026, 10, 30, 15, 59, 0, 0, time.UTC), "10\u00a0月 30\u00a0日"},
		{"mid-day end", i18n.ZhHant, now, time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC), "10\u00a0月 30\u00a0日"},
		{"29 days", i18n.ZhHant, now, now.AddDate(0, 0, 29), "10\u00a0月 31\u00a0日"},
		{"30 days", i18n.ZhHant, now, now.AddDate(0, 0, 30), "11\u00a0月 1\u00a0日"},
		{"31 days", i18n.ZhHant, now, now.AddDate(0, 0, 31), "11\u00a0月 2\u00a0日"},
		{"29 days in English", i18n.En, now, now.AddDate(0, 0, 29), "Oct\u00a031"},
		{"31 days in English", i18n.En, now, now.AddDate(0, 0, 31), "Nov\u00a02"},
		{"a year", i18n.ZhHant, now, now.AddDate(1, 0, 0), "2027\u00a0年 10\u00a0月 2\u00a0日"},
		{"into next year", i18n.ZhHant, december, time.Date(2027, 1, 5, 4, 0, 0, 0, time.UTC), "2027\u00a0年 1\u00a0月 5\u00a0日"},
		{"into next year in English", i18n.En, december, time.Date(2027, 1, 5, 4, 0, 0, 0, time.UTC), "Jan\u00a05, 2027"},
	} {
		got := CampaignEndsOn(i18n.WithLocale(t.Context(), tt.locale), tt.endsAt, tt.now)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTheOffersStripNamesOnlyANearLastDay(t *testing.T) {
	t.Parallel()

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
