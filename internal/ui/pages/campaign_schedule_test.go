package pages

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// 12:00 on 2 October 2026 in Taipei.
var scheduleNow = time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)

func day(d int) time.Time { return time.Date(2026, 10, d-1, 16, 0, 0, 0, time.UTC) }

func renderCampaign(t *testing.T, startsAt, endsAt time.Time, struck bool) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	tile := ProductTile{Slug: "p", Name: "茶壺", PriceCents: 80000, InStock: true}
	if struck {
		tile.CompareCents = 100000
		tile.InCampaign = CampaignStateAt(startsAt, endsAt, scheduleNow) == CampaignRunning
	}
	return renderComponent(t, ctx, Campaign(layouts.Page{Title: "c"}, CampaignView{
		Slug: "c", Title: "秋日選物", Products: []ProductTile{tile},
		Schedule: NewCampaignSchedule(ctx, "秋日選物", 1, startsAt, endsAt, scheduleNow),
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
		{"running", day(1), day(11), []string{"<dt>商品</dt>", "<dt>結束</dt>", "<dt>剩餘</dt>", "8\u00a0<small>天</small>"}, []string{"開始", "已結束"}, 1},
		{"one day left", day(1), day(4), []string{"明天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"last day", day(1), day(3), []string{"今天結束"}, []string{"<dt>剩餘</dt>", "已結束"}, 1},
		{"not started", day(5), day(9), []string{"<dt>開始</dt>", "<dt>結束</dt>", "<dt>商品</dt>", "10\u00a0月 5\u00a0日", "還沒開始"}, []string{"已結束", "<dt>剩餘</dt>", `data-cell`}, 0},
		{"ended", day(-8), day(2), []string{"已結束", "<dt>結束</dt>", "已結束。"}, []string{"<dt>開始</dt>", "<dt>剩餘</dt>", `data-cell="today"`}, 10},
	}
	for _, tt := range tests {
		got := renderCampaign(t, tt.startsAt, tt.endsAt, false)
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

func TestACampaignOutsideItsWindowDoesNotStrikeAPrice(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		startsAt time.Time
		endsAt   time.Time
		struck   bool
	}{
		{"running", day(1), day(11), true},
		{"not started", day(5), day(9), false},
		{"ended", day(-8), day(2), false},
	} {
		got := strings.Contains(renderCampaign(t, tt.startsAt, tt.endsAt, true), "goen-tile__was")
		if got != tt.struck {
			t.Errorf("%s campaign strikes a price = %v, want %v", tt.name, got, tt.struck)
		}
	}
}

func TestNothingOfferedIsCalledLimitedTime(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, text := range map[string]string{
		"nav":              i18n.T(ctx, i18n.KeyDeals),
		"deals title":      DealsMeta(ctx).Title,
		"deals page":       renderComponent(t, ctx, Deals(DealsMeta(ctx), SearchView{})),
		"campaign meta":    CampaignMeta(ctx, "秋日選物", Photo{}).Description,
		"campaign page":    renderCampaign(t, day(1), day(11), false),
		"campaigns strip":  renderComponent(t, ctx, campaignStrip([]CampaignSummary{{Slug: "a", Title: "秋日選物", Products: 2}})),
		"campaigns header": i18n.T(ctx, i18n.KeyCampaignsRunning),
		"department notice": renderComponent(t, ctx, departmentNotice(&DepartmentNotice{
			Title: "秋日選物", Href: "/s/autumn", End: NewCampaignEnd(day(11), scheduleNow),
		})),
	} {
		if strings.Contains(text, "限時") {
			t.Errorf("%s says 限時", name)
		}
	}
	if got := DealsMeta(ctx).Title; got != "優惠" {
		t.Errorf("DealsMeta title = %q, want 優惠", got)
	}
}

func TestACampaignIsCalledACampaignInEnglish(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for key, want := range map[i18n.Key]string{
		i18n.KeyCampaignNotFound:     "Campaign not found",
		i18n.KeyCampaignNotFoundBody: "We could not find that campaign. See the deals running now.",
	} {
		if got := i18n.T(ctx, key); got != want {
			t.Errorf("i18n.T(%q) = %q, want %q", key, got, want)
		}
	}
	if got := CampaignMeta(ctx, "Autumn", Photo{}).Description; got != "Autumn — deals at goen" {
		t.Errorf("CampaignMeta description = %q, want %q", got, "Autumn — deals at goen")
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
		schedule := NewCampaignSchedule(ctx, "秋日選物", 3, day(1), tt.endsAt, scheduleNow)
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
