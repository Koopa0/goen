package pages

import (
	"strings"
	"testing"
)

func TestCampaignPaginationKeepsBothBrowsingPositions(t *testing.T) {
	v := SearchView{Path: "/deals", Page: 3, PageSize: 24, Total: 100, Campaigns: CampaignPage{Page: 2, PageSize: 6, Total: 13, Rows: []CampaignSummary{{Slug: "seventh", Title: "Seventh campaign"}}}}
	if got := v.CampaignPageHref(3); got != "/deals?campaign_page=3&page=3#campaigns" {
		t.Errorf("campaign href = %s", got)
	}
	if got := v.PageHref(4); got != "/deals?campaign_page=2&page=4" {
		t.Errorf("product href = %s", got)
	}
	rendered := renderToString(t, Deals(DealsMeta(t.Context()), v))
	for _, want := range []string{`href="/s/seventh"`, `href="/deals?campaign_page=3&amp;page=3#campaigns"`, `href="/deals?page=3#campaigns"`} {
		if !strings.Contains(rendered, want) {
			t.Errorf("campaign pager omits %s", want)
		}
	}
}
