package home

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/hero"
	"github.com/koopa0/goen/internal/ui/tone"
)

// TestStore_CampaignSlideEnds holds that a campaign slide says its period in one
// phrase, not as figures: the last day and the days left, then on the last two
// days only that, with the time of day where it is not midnight. The track of
// days and the button stay.
func TestStore_CampaignSlideEnds(t *testing.T) {
	t.Parallel()

	taipei := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, taipei)
	s := &Store{now: func() time.Time { return now }}
	for _, tt := range []struct {
		name     string
		locale   i18n.Locale
		endsAt   time.Time
		want     string
		datetime string
	}{
		{"three days, ending at six", i18n.ZhHant, time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), "至10\u00a0月12\u00a0日18:00&nbsp;·剩\u00a03\u00a0天", `<time datetime="2026-10-12T18:00">`},
		{"three days, ending at six", i18n.En, time.Date(2026, 10, 12, 18, 0, 0, 0, taipei), "untilOct\u00a012at18:00,&nbsp;·3\u00a0daysleft", `<time datetime="2026-10-12T18:00">`},
		{"ends tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), "明天結束", `<time datetime="2026-10-10">`},
		{"ends tomorrow", i18n.En, time.Date(2026, 10, 11, 0, 0, 0, 0, taipei), "endstomorrow", `<time datetime="2026-10-10">`},
		{"ends today", i18n.ZhHant, time.Date(2026, 10, 10, 0, 0, 0, 0, taipei), "今天結束", `<time datetime="2026-10-09">`},
		{"ends today at six", i18n.ZhHant, time.Date(2026, 10, 9, 18, 0, 0, 0, taipei), "今天18:00結束", `<time datetime="2026-10-09T18:00">`},
		{"ends today at six", i18n.En, time.Date(2026, 10, 9, 18, 0, 0, 0, taipei), "endstodayat18:00", `<time datetime="2026-10-09T18:00">`},
	} {
		t.Run(tt.name+" "+string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			slide := s.campaignSlide(ctx, &db.ListedCampaignsRow{
				Slug: "autumn", Title: "A campaign whose name runs on past one line of a narrow phone, 年終感謝祭全館滿額再折",
				StartsAt: now.AddDate(0, 0, -3), EndsAt: tt.endsAt, Products: 6,
			})
			page := renderComponent(t, ctx, heroCarousel([]hero.Slide{slide}))
			start := strings.Index(page, `<p class="goen-hero__ends">`)
			if start < 0 {
				t.Fatalf("the campaign slide has no phrase of its end:\n%s", page)
			}
			when := page[start : start+strings.Index(page[start:], "</p>")]
			if got := text(when); !strings.Contains(got, tt.want) {
				t.Errorf("the slide says %q, want it to hold %q", got, tt.want)
			}
			if !strings.Contains(when, tt.datetime) {
				t.Errorf("the phrase lacks %s: %s", tt.datetime, when)
			}
			for _, absent := range []string{"<dt>", "ui-statline"} {
				if strings.Contains(page, absent) {
					t.Errorf("the campaign slide draws %s; it says its period in one phrase", absent)
				}
			}
			for _, want := range []string{`class="ui-period"`, `href="/s/autumn"`} {
				if !strings.Contains(page, want) {
					t.Errorf("the campaign slide lost %s", want)
				}
			}
		})
	}
}

// TestCarouselSources_ToneOf holds that a scheduled slide takes the tone of
// what it links to: a campaign's, a department's, and stone for any other or
// unknown link.
func TestCarouselSources_ToneOf(t *testing.T) {
	t.Parallel()

	src := carouselSources{
		camps: []db.ListedCampaignsRow{{Slug: "autumn", Tone: "sage"}},
		cats:  []db.RootCategoriesRow{{Slug: "tech", Tone: "mist"}},
	}
	for href, want := range map[string]tone.Tone{
		"/s/autumn": tone.Sage, "/c/tech": tone.Mist, "/about": tone.Stone, "/s/gone": tone.Stone,
	} {
		if got := src.toneOf(href); got != want {
			t.Errorf("toneOf(%q) = %q, want %q", href, got, want)
		}
	}
}

var tag = regexp.MustCompile(`<[^>]*>|[ \n\t]+`)

// text is the rendered markup with its tags and its plain spacing taken out.
func text(markup string) string { return tag.ReplaceAllString(markup, "") }
