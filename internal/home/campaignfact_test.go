package home

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
)

// The campaign card says what is left before when it ends, and leaves the count
// to its link; on the last two days only the end's note remains.
func TestTheCampaignCardStatesWhatIsLeftThenTheEnd(t *testing.T) {
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
		{"three days", time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), []string{"剩餘3\u00a0天", "結束10\u00a0月12\u00a0日\u00a018:00"}},
		{"last day", time.Date(2026, 10, 10, 0, 0, 0, 0, taipei), []string{"結束10\u00a0月9\u00a0日今天結束"}},
	} {
		schedule := s.campaignSchedule(ctx, &db.ListedCampaignsRow{EndsAt: tt.endsAt, Products: 6})
		stats := schedule.CardFacts()
		got := make([]string, 0, len(stats))
		for i := range stats {
			var b strings.Builder
			if err := components.StatLine(stats[i:i+1], components.StatLinePlain).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			got = append(got, text(b.String()))
		}
		if !slices.Equal(got, tt.want) {
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
		{"three days", time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), []string{"商品6\u00a0件", "結束10\u00a0月12\u00a0日\u00a018:00", "剩餘3\u00a0天"}},
		{"midnight end, last day tomorrow", time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), []string{"商品6\u00a0件", "結束10\u00a0月10\u00a0日明天結束"}},
		{"midnight end, last day today", time.Date(2026, 10, 10, 0, 0, 0, 0, taipei), []string{"商品6\u00a0件", "結束10\u00a0月9\u00a0日今天結束"}},
		{"today at 18:00", time.Date(2026, 10, 9, 18, 0, 0, 0, taipei), []string{"商品6\u00a0件", "結束10\u00a0月9\u00a0日\u00a018:00今天結束"}},
	} {
		stats := s.campaignStats(ctx, &db.ListedCampaignsRow{EndsAt: tt.endsAt, Products: 6})
		got := make([]string, 0, len(stats))
		for i := range stats {
			var b strings.Builder
			if err := components.StatLine(stats[i:i+1], components.StatLinePlain).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			got = append(got, text(b.String()))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestAScheduledSlideTakesTheToneOfWhatItLinksTo(t *testing.T) {
	t.Parallel()

	src := carouselSources{
		camps: []db.ListedCampaignsRow{{Slug: "autumn", Tone: "sage"}},
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

var tag = regexp.MustCompile(`<[^>]*>|[ \n\t]+`)

// text is the rendered markup with its tags and its plain spacing taken out.
func text(markup string) string { return tag.ReplaceAllString(markup, "") }

// The end date is a time element, so a machine reads the day and the clock too.
func TestTheEndDateIsATimeElement(t *testing.T) {
	t.Parallel()

	taipei := time.FixedZone("CST", 8*3600)
	s := &Store{now: func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, taipei) }}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, tt := range map[string]struct {
		endsAt time.Time
		want   string
	}{
		"midnight end": {time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), `<time datetime="2026-10-10">`},
		"mid-day end":  {time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), `<time datetime="2026-10-12T18:00">`},
	} {
		var b strings.Builder
		stats := s.campaignStats(ctx, &db.ListedCampaignsRow{EndsAt: tt.endsAt, Products: 6})
		if err := components.StatLine(stats, components.StatLinePlain).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), tt.want) {
			t.Errorf("%s: %s lacks %s", name, b.String(), tt.want)
		}
	}
}
