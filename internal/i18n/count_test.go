package i18n

import (
	"testing"
)

func TestCountReadsTheSingularOnlyForOne(t *testing.T) {
	cases := []struct {
		locale Locale
		key    Key
		n      int64
		shown  any
		want   string
	}{
		{En, "pdp.reviews.stars", 1, "1", "1 star"},
		{En, "pdp.reviews.stars", 2, "2", "2 stars"},
		{En, "pdp.reviews.stars", 0, "0", "0 stars"},
		{En, "pdp.reviews.count", 1, "1", "1 review"},
		{En, "campaign.products", 1, "1", "1 product"},
		{En, "campaign.products", 1000, "1,000", "1,000 products"},
		{En, "deals.count", 1, "1", "1 product reduced"},
		{En, "campaign.ends.hours", 1, int64(1), "1 hour left"},
		{En, "campaign.ends.hours", 5, int64(5), "5 hours left"},
		{En, "campaign.ends.days", 1, int64(1), "1 day left"},
		{En, "campaign.ends.days", 2, int64(2), "2 days left"},
		{ZhHant, "campaign.ends.days", 1, int64(1), "剩 1 天"},
		{ZhHant, "pdp.reviews.stars", 1, "1", "1 星"},
	}
	for _, c := range cases {
		if got := Count(WithLocale(t.Context(), c.locale), c.key, c.n, c.shown); got != c.want {
			t.Errorf("Count(%s, %s, %d) = %q, want %q", c.locale, c.key, c.n, got, c.want)
		}
	}
}
