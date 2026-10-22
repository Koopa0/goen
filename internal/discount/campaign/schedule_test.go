package campaign_test

import (
	"context"
	"slices"
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

// TestSchedule_CardFacts: card facts put days left before the end and omit it
// in the last two days.
func TestSchedule_CardFacts(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name   string
		endsAt time.Time
		want   []string
	}{
		{"days left", day(11), []string{"剩餘", "結束"}},
		{"tomorrow", day(4), []string{"結束"}},
	} {
		schedule := campaign.NewSchedule(ctx, "秋日選物", 3, day(1), tt.endsAt, scheduleNow)
		facts := schedule.CardFacts()
		got := make([]string, 0, len(facts))
		for i := range facts {
			got = append(got, facts[i].Label)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: CardFacts labels = %q, want %q", tt.name, got, tt.want)
		}
		if tt.name == "tomorrow" && facts[0].Note != "明天結束" {
			t.Errorf("tomorrow: the end's note = %q, want 明天結束", facts[0].Note)
		}
	}
}

// TestNewScheduleFacts: a fact names what it counts once: each rendered fact
// line names its noun once, in both locales.
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
			name: "campaign",
			facts: func(ctx context.Context) []widget.Stat {
				return campaign.NewSchedule(ctx, "Autumn", 12, now.AddDate(0, 0, -1), ends, now).Facts
			},
			want: map[i18n.Locale][]string{
				i18n.En:     {"<dt>Items</dt><dd>12</dd>", "<dt>Days left</dt><dd>7</dd>"},
				i18n.ZhHant: {"<dt>商品</dt>", "12\u00a0<small>件</small>", "<dt>剩餘</dt>", "7\u00a0<small>天</small>"},
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
