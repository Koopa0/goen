package campaign_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/campaign"
)

func TestStateAtJudgesTheWindowByTheInstant(t *testing.T) {
	t.Parallel()
	start, end := day(2), day(12)
	taipei := time.FixedZone("Asia/Taipei", 8*60*60)
	for _, tt := range []struct {
		name string
		now  time.Time
		want campaign.State
	}{
		{"a nanosecond before the start", start.Add(-time.Nanosecond), campaign.NotStarted},
		{"at the start", start, campaign.Running},
		{"at the start, read on the shop's clock", start.In(taipei), campaign.Running},
		{"a nanosecond before the end", end.Add(-time.Nanosecond), campaign.Running},
		{"at the end, which is exclusive", end, campaign.Ended},
		{"at the end, read on the shop's clock", end.In(taipei), campaign.Ended},
		{"after the end", end.Add(time.Hour), campaign.Ended},
	} {
		if got := campaign.StateAt(start, end, tt.now); got != tt.want {
			t.Errorf("%s: StateAt(%v, %v, %v) = %q, want %q", tt.name, start, end, tt.now, got, tt.want)
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
		if got := campaign.OnSale(tt.featured, tt.buyable, tt.price, tt.compare); got != tt.want {
			t.Errorf("%s: OnSale(%v, %v, %d, %d) = %v, want %v",
				tt.name, tt.featured, tt.buyable, tt.price, tt.compare, got, tt.want)
		}
	}
}
