package catalog_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
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

func TestCampaignStateAtJudgesTheWindowByTheInstant(t *testing.T) {
	t.Parallel()
	start, end := day(2), day(12)
	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	for _, tt := range []struct {
		name string
		now  time.Time
		want catalog.CampaignState
	}{
		{"a nanosecond before the start", start.Add(-time.Nanosecond), catalog.CampaignNotStarted},
		{"at the start", start, catalog.CampaignRunning},
		{"at the start, read on the shop's clock", start.In(taipei), catalog.CampaignRunning},
		{"a nanosecond before the end", end.Add(-time.Nanosecond), catalog.CampaignRunning},
		{"at the end, which is exclusive", end, catalog.CampaignEnded},
		{"at the end, read on the shop's clock", end.In(taipei), catalog.CampaignEnded},
		{"after the end", end.Add(time.Hour), catalog.CampaignEnded},
	} {
		if got := catalog.CampaignStateAt(start, end, tt.now); got != tt.want {
			t.Errorf("%s: CampaignStateAt(%v, %v, %v) = %q, want %q", tt.name, start, end, tt.now, got, tt.want)
		}
	}
}

func TestOnSaleNeedsACampaignStockAndAHigherComparePrice(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name              string
		featured, buyable bool
		price, compare    int64
		want              bool
	}{
		{"featured, in stock and reduced", true, true, 43200, 48000, true},
		{"reduced by the least amount", true, true, 43200, 43201, true},
		{"compare-at equal to the price", true, true, 43200, 43200, false},
		{"compare-at below the price", true, true, 43200, 40000, false},
		{"full price: no compare-at", true, true, 43200, 0, false},
		{"sold out", true, false, 43200, 48000, false},
		{"featured by no running campaign", false, true, 43200, 48000, false},
	} {
		if got := catalog.OnSale(tt.featured, tt.buyable, tt.price, tt.compare); got != tt.want {
			t.Errorf("%s: OnSale(%v, %v, %d, %d) = %v, want %v",
				tt.name, tt.featured, tt.buyable, tt.price, tt.compare, got, tt.want)
		}
	}
}

func renderCampaign(t *testing.T, startsAt, endsAt time.Time) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	return renderComponent(t, ctx, pages.Campaign(layouts.Page{Title: "c"}, pages.CampaignView{
		Slug: "c", Title: "秋日選物", Products: []pages.ProductTile{{Slug: "p", Name: "茶壺", PriceCents: 80000, InStock: true}},
		Schedule: catalog.NewCampaignSchedule(ctx, "秋日選物", 1, startsAt, endsAt, scheduleNow),
	}))
}

func TestACampaignPageStatesItsFactsAndDrawsItsDays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		startsAt     time.Time
		endsAt       time.Time
		want, absent []string
		filled       int
	}{
		{"running", day(1), day(11), []string{"<dt>商品</dt>", "<dt>結束</dt>", "<dt>剩餘</dt>", "8\u00a0<small>天</small>"}, []string{"開始", "已結束", "限時"}, 1},
		{"one day left", day(1), day(4), []string{"明天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"last day", day(1), day(3), []string{"今天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"not started", day(5), day(9), []string{"<dt>開始</dt>", "<dt>結束</dt>", "<dt>商品</dt>", "10\u00a0月 5\u00a0日", "還沒開始"}, []string{"已結束", "<dt>剩餘</dt>", `data-cell`}, 0},
		{"ended", day(-8), day(2), []string{"已結束", "<dt>結束</dt>", "已結束。"}, []string{"<dt>開始</dt>", "<dt>剩餘</dt>", `data-cell="today"`}, 10},
	}
	for _, tt := range tests {
		got := renderCampaign(t, tt.startsAt, tt.endsAt)
		for _, want := range tt.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: page lacks %q", tt.name, want)
			}
		}
		for _, absent := range tt.absent {
			if strings.Contains(got, absent) {
				t.Errorf("%s: page holds %q", tt.name, absent)
			}
		}
		if !strings.Contains(got, `class="ui-period"`) {
			t.Errorf("%s: page draws no day grid", tt.name)
		}
		if n := strings.Count(got, `data-cell="past"`); tt.name == "ended" && n != tt.filled {
			t.Errorf("ended: %d filled cells, want %d", n, tt.filled)
		}
	}
}

func TestCardFactsPutDaysLeftBeforeTheEndAndOmitItInTheLastTwoDays(t *testing.T) {
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
		schedule := catalog.NewCampaignSchedule(ctx, "秋日選物", 3, day(1), tt.endsAt, scheduleNow)
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

func TestAFactNamesWhatItCountsOnce(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name  string
		facts func(ctx context.Context) []components.Stat
		want  map[i18n.Locale][]string
	}{
		{
			name: "campaign",
			facts: func(ctx context.Context) []components.Stat {
				return catalog.NewCampaignSchedule(ctx, "Autumn", 12, now.AddDate(0, 0, -1), ends, now).Facts
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
				got := renderComponent(t, ctx, components.StatLine(tt.facts(ctx), components.StatLinePlain))
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
