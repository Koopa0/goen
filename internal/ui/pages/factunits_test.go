package pages

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

func TestAFactNamesWhatItCountsOnce(t *testing.T) {
	t.Parallel()
	ends := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	head := &DepartmentHead{Products: 20, Categories: 6, Brands: 5}
	for _, tt := range []struct {
		name  string
		facts func(ctx context.Context) []components.Stat
		want  map[i18n.Locale][]string
	}{
		{
			name:  "department head",
			facts: head.Facts,
			want: map[i18n.Locale][]string{
				i18n.En:     {"<dt>Items</dt><dd>20</dd>", "<dt>Categories</dt><dd>6</dd>", "<dt>Brands</dt><dd>5</dd>"},
				i18n.ZhHant: {"<dt>商品</dt>", "<dt>分類</dt>", "<dt>品牌</dt>", "20\u00a0<small>件</small>", "6\u00a0<small>類</small>", "5\u00a0<small>個</small>"},
			},
		},
		{
			name: "campaign",
			facts: func(ctx context.Context) []components.Stat {
				return NewCampaignSchedule(ctx, "Autumn", 12, now.AddDate(0, 0, -1), ends, now).Facts
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
