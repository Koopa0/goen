package home

import (
	"slices"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// A campaign row names its last day however far off it is.
func TestACampaignRowFactNamesItsLastDay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	s := &Store{now: func() time.Time { return now }}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name   string
		endsAt time.Time
		want   string
	}{
		{"near", now.AddDate(0, 0, 29), "6 件商品，至 10\u00a0月 31\u00a0日"},
		{"a year off", now.AddDate(1, 0, 0), "6 件商品，至 2027\u00a0年 10\u00a0月 2\u00a0日"},
	} {
		got := s.campaignRowFact(ctx, &db.HomeCampaignsRow{EndsAt: tt.endsAt, Products: 6})
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// From two days out a campaign slide states the days left; on the last two days
// the end's note says so instead, and an end off midnight states its time.
func TestACampaignSlideStatesWhatTheShopperNeeds(t *testing.T) {
	t.Parallel()

	taipei := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, taipei)
	s := &Store{now: func() time.Time { return now }}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name   string
		endsAt time.Time
		want   []string
	}{
		{"three days", time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), []string{"商品=6\u00a0件", "結束=10\u00a0月 12\u00a0日 18:00", "剩餘=3\u00a0天"}},
		{"midnight end, last day tomorrow", time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), []string{"商品=6\u00a0件", "結束=10\u00a0月 10\u00a0日 明天結束"}},
		{"midnight end, last day today", time.Date(2026, 10, 10, 0, 0, 0, 0, taipei), []string{"商品=6\u00a0件", "結束=10\u00a0月 9\u00a0日 今天結束"}},
		{"today at 18:00", time.Date(2026, 10, 9, 18, 0, 0, 0, taipei), []string{"商品=6\u00a0件", "結束=10\u00a0月 9\u00a0日 18:00 今天結束"}},
	} {
		var got []string
		for _, st := range s.campaignStats(ctx, &db.HomeCampaignsRow{EndsAt: tt.endsAt, Products: 6}) {
			line := st.Label + "=" + st.Value
			if st.Note != "" {
				line += " " + st.Note
			}
			got = append(got, line)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestAScheduledSlideTakesTheToneOfWhatItLinksTo(t *testing.T) {
	t.Parallel()

	src := carouselSources{
		camps: []db.HomeCampaignsRow{{Slug: "autumn", Tone: "sage"}},
		cats:  []db.RootCategoriesRow{{Slug: "tech", Tone: "mist"}},
	}
	for href, want := range map[string]pages.Tone{
		"/s/autumn": pages.ToneSage, "/c/tech": pages.ToneMist, "/about": pages.ToneStone, "/s/gone": pages.ToneStone,
	} {
		if got := src.toneOf(href); got != want {
			t.Errorf("toneOf(%q) = %q, want %q", href, got, want)
		}
	}
}
