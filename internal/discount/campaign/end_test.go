package campaign_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestEndsOn: a campaign names its last day however far off.
func TestEndsOn(t *testing.T) {
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
		got := campaign.EndsOn(i18n.WithLocale(t.Context(), tt.locale), tt.endsAt, tt.now)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestEndStat: the campaign end stat names its last day and the last two days.
func TestEndStat(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	got := renderComponent(t, ctx, widget.StatLine([]widget.Stat{campaign.EndStat(ctx, ends, now)}, widget.StatLinePlain))
	for _, want := range []string{`datetime="2026-10-10"`, "明天結束"} {
		if !strings.Contains(got, want) {
			t.Errorf("end stat omits %s:\n%s", want, got)
		}
	}
}

// TestEnds holds the phrase every place that says a campaign's end shares: the
// day and its dot are one unit, what is left follows it, the last two days say
// so whole, and what the caller passes stays inside the last words, so no line
// can wrap to begin with a dot or an arrow. The test reads the markup, where the
// no-break space between the day and its dot is the entity.
func TestEnds(t *testing.T) {
	t.Parallel()
	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, cst)
	for _, tt := range []struct {
		name     string
		locale   i18n.Locale
		endsAt   time.Time
		want     string
		datetime string
		dot      bool
	}{
		{"days left", i18n.ZhHant, time.Date(2026, 10, 19, 0, 0, 0, 0, cst), "至 10\u00a0月 18\u00a0日&nbsp;· 剩\u00a09\u00a0天→", "2026-10-18", true},
		{"days left", i18n.En, time.Date(2026, 10, 19, 0, 0, 0, 0, cst), "until Oct\u00a018,&nbsp;· 9\u00a0days left→", "2026-10-18", true},
		{"two days left", i18n.En, time.Date(2026, 10, 12, 0, 0, 0, 0, cst), "until Oct\u00a011,&nbsp;· 2\u00a0days left→", "2026-10-11", true},
		{"tomorrow", i18n.ZhHant, time.Date(2026, 10, 11, 0, 0, 0, 0, cst), "明天結束→", "2026-10-10", false},
		{"today", i18n.En, time.Date(2026, 10, 10, 0, 0, 0, 0, cst), "ends today→", "2026-10-09", false},
		{"today at six", i18n.ZhHant, time.Date(2026, 10, 9, 18, 0, 0, 0, cst), "今天 18:00 結束→", "2026-10-09T18:00", false},
	} {
		t.Run(tt.name+" "+string(tt.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			var b strings.Builder
			if err := campaign.Ends(campaign.NewEnd(tt.endsAt, now)).Render(templ.WithChildren(ctx, templ.Raw("→")), &b); err != nil {
				t.Fatal(err)
			}
			got := b.String()
			if text := tag.ReplaceAllString(got, ""); strings.TrimSpace(text) != tt.want {
				t.Errorf("the phrase reads %q, want %q", text, tt.want)
			}
			if !strings.Contains(got, `<time datetime="`+tt.datetime+`">`) {
				t.Errorf("the phrase lacks a time element for %s:\n%s", tt.datetime, got)
			}
			if !strings.HasSuffix(got, "→</span>") {
				t.Errorf("the children do not close the last words:\n%s", got)
			}
			if tt.dot {
				if !strings.Contains(got, `&nbsp;<span class="goen-ends__dot" aria-hidden="true">·</span></span>`) {
					t.Errorf("the dot does not follow a no-break space inside the day's unit:\n%s", got)
				}
			} else if strings.Contains(got, "·") || strings.Contains(got, "goen-ends__until") {
				t.Errorf("the last two days draw a day and a dot:\n%s", got)
			}
		})
	}
}

var tag = regexp.MustCompile(`<[^>]*>`)
