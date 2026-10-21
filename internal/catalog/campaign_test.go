package catalog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tile"
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

func renderCampaign(t *testing.T, startsAt, endsAt time.Time) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	return renderComponent(t, ctx, catalog.Campaign(layouts.Page{Title: "c"}, catalog.CampaignView{
		Slug: "c", Title: "秋日選物", Products: []tile.Tile{{Slug: "p", Name: "茶壺", PriceCents: 80000, InStock: true}},
		Schedule: campaign.NewSchedule(ctx, "秋日選物", 1, startsAt, endsAt, scheduleNow),
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
