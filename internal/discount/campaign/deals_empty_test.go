package campaign

import (
	"strings"
	"testing"
)

// TestDealsEmptyIsAPageOnlyWithoutCampaigns holds that /deals is an empty
// page only when nothing at all is on it. Total counts discounted products
// and is independent of the running campaigns, so under a campaign strip the
// same sentence is one line of a section.
func TestDealsEmptyIsAPageOnlyWithoutCampaigns(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		view      DealsView
		wantEmpty bool
	}{
		{"no campaigns, no products", DealsView{}, true},
		{"campaigns, no products", DealsView{Campaigns: Listed{Rows: []Summary{{Slug: "autumn", Title: "秋日選物"}}, Page: 1, Total: 1, PageSize: 10}}, false},
	} {
		page := renderToString(t, Deals(DealsMeta(t.Context()), tt.view))
		if got := strings.Contains(page, `class="goen-empty"`); got != tt.wantEmpty {
			t.Errorf("%s: goen-empty box = %t, want %t", tt.name, got, tt.wantEmpty)
		}
		if strings.Contains(page, "goen-notice") {
			t.Errorf("%s: the empty sentence is drawn as a notice", tt.name)
		}
		if !strings.Contains(page, "目前沒有正在特價的商品") {
			t.Errorf("%s: the page does not say nothing is on offer", tt.name)
		}
	}
}
