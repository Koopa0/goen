package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
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

// navLabelled returns the pager <nav> carrying the given aria-label.
func navLabelled(t *testing.T, html, label string) string {
	t.Helper()
	start := strings.Index(html, `<nav class="goen-pager" aria-label="`+label+`"`)
	if start < 0 {
		t.Fatalf("no pager labelled %q", label)
	}
	end := strings.Index(html[start:], "</nav>")
	if end < 0 {
		t.Fatalf("pager labelled %q is not closed", label)
	}
	return html[start : start+end]
}

func TestCampaignPagerKeepsItsEndsVisibleAndTheProductPagerItsLabel(t *testing.T) {
	ctx := t.Context()
	campaign, product := i18n.T(ctx, i18n.KeyCampaignPagination), i18n.T(ctx, i18n.KeyPagination)
	for _, tt := range []struct {
		page      int
		disabled  string
		mustLinks string
	}{{1, i18n.T(ctx, i18n.KeyPrevPage), "campaign_page=2"}, {3, i18n.T(ctx, i18n.KeyNextPage), "campaign_page=2"}} {
		v := SearchView{Path: "/deals", Page: 3, PageSize: 24, Total: 100, Campaigns: CampaignPage{Page: tt.page, PageSize: 6, Total: 13, Rows: []CampaignSummary{{Slug: "seventh", Title: "Seventh campaign"}}}}
		html := renderToString(t, Deals(DealsMeta(ctx), v))
		nav := navLabelled(t, html, campaign)
		if want := `aria-disabled="true">` + tt.disabled + `</span>`; !strings.Contains(nav, want) {
			t.Errorf("campaign page %d of 3 lacks a disabled %s: %s", tt.page, tt.disabled, nav)
		}
		if !strings.Contains(nav, tt.mustLinks) {
			t.Errorf("campaign page %d of 3 does not link to %s", tt.page, tt.mustLinks)
		}
		if !strings.Contains(navLabelled(t, html, product), "rel=\"next\"") {
			t.Error("product pager lost its Next link")
		}
	}
}
