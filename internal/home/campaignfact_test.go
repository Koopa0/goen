package home

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

// The hero and the campaign row share one rule for the last day: named within
// 30 days, left out beyond, with the product count either way.
func TestACampaignFactNamesOnlyANearLastDay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)
	s := &Store{now: func() time.Time { return now }}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name   string
		endsAt time.Time
		key    i18n.Key
		want   string
	}{
		{"hero, near", now.AddDate(0, 0, 29), i18n.KeyHomeCampaignFact, "6 件商品 · 至 10\u00a0月 31\u00a0日"},
		{"row, near", now.AddDate(0, 0, 29), i18n.KeyHomeCampaignRowFact, "6 件商品，至 10\u00a0月 31\u00a0日"},
		{"hero, a year off", now.AddDate(1, 0, 0), i18n.KeyHomeCampaignFact, "6 件商品"},
		{"row, a year off", now.AddDate(1, 0, 0), i18n.KeyHomeCampaignRowFact, "6 件商品"},
	} {
		got := s.campaignFact(ctx, &db.HomeCampaignsRow{EndsAt: tt.endsAt, Products: 6}, tt.key)
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
