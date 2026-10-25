package campaign_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// 12:00 on 2 October 2026 in Taipei.
var scheduleNow = time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)

// day is the start of a shop day of October 2026.
func day(d int) time.Time { return time.Date(2026, 10, d-1, 16, 0, 0, 0, time.UTC) }

func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestNewScheduleFacts: a fact names what it counts once: each rendered fact
// line of a campaign not yet started names its noun once, in both locales.
func TestNewScheduleFacts(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name  string
		facts func(ctx context.Context) []widget.Stat
		want  map[i18n.Locale][]string
	}{
		{
			name: "campaign not started",
			facts: func(ctx context.Context) []widget.Stat {
				return campaign.NewSchedule(ctx, "Autumn", 12, now.AddDate(0, 0, 1), ends, now).Facts
			},
			want: map[i18n.Locale][]string{
				i18n.En:     {"<dt>Items</dt><dd>12</dd>"},
				i18n.ZhHant: {"<dt>商品</dt>", "12\u00a0<small>件</small>"},
			},
		},
	} {
		for locale, wants := range tt.want {
			t.Run(tt.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				got := renderComponent(t, ctx, widget.StatLine(tt.facts(ctx), widget.StatLinePlain))
				for _, want := range wants {
					if !strings.Contains(got, want) {
						t.Errorf("fact line omits %q:\n%s", want, got)
					}
				}
				if locale == i18n.En {
					for _, noun := range []string{"items", "categories", "brands", "days"} {
						if n := strings.Count(strings.ToLower(got), noun); n > 1 {
							t.Errorf("fact line writes %q %d times:\n%s", noun, n, got)
						}
					}
				}
			})
		}
	}
}

// TestNewScheduleStates holds that only a running campaign has the phrase of its
// end: one not started and one ended keep the fact line, and an ended campaign
// never says it ends today.
func TestNewScheduleStates(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name         string
		startsAt     time.Time
		endsAt       time.Time
		wantEnd      bool
		wantFactsLen int
	}{
		{"running", day(1), day(11), true, 0},
		{"last day", day(1), day(3), true, 0},
		{"not started", day(5), day(9), false, 3},
		{"ended", day(-8), day(2), false, 2},
	} {
		got := campaign.NewSchedule(ctx, "秋日選物", 4, tt.startsAt, tt.endsAt, scheduleNow)
		if (got.End != nil) != tt.wantEnd || len(got.Facts) != tt.wantFactsLen {
			t.Errorf("%s: end set %v with %d facts, want %v and %d", tt.name, got.End != nil, len(got.Facts), tt.wantEnd, tt.wantFactsLen)
		}
	}
}
