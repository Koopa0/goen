package campaign

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// renderToString renders c in Traditional Chinese.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestDealsView_PageHref: campaign pagination keeps both browsing positions: a
// campaign page link keeps the product page, and a product page link keeps the
// campaign page.
func TestDealsView_PageHref(t *testing.T) {
	v := DealsView{Page: 3, PageSize: 24, Total: 100, Campaigns: Listed{Page: 2, PageSize: ListedPageSize, Total: 2*ListedPageSize + 1, Rows: []Summary{{Slug: "seventh", Title: "Seventh campaign"}}}}
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

// TestListedPageSizeFillsRows: a page of the strip ends on a full row at every
// column count the strip's grid reaches, so no page stops short.
func TestListedPageSizeFillsRows(t *testing.T) {
	t.Parallel()
	for _, columns := range []int{2, 3, 4} {
		if ListedPageSize%columns != 0 {
			t.Errorf("a page of %d campaigns ends on a short row at %d columns", ListedPageSize, columns)
		}
	}
}

// TestDealsPagers: the campaign pager keeps its ends visible and the product
// pager its label.
func TestDealsPagers(t *testing.T) {
	ctx := t.Context()
	campaigns, product := i18n.T(ctx, i18n.KeyCampaignPagination), i18n.T(ctx, i18n.KeyPagination)
	for _, tt := range []struct {
		page      int
		disabled  string
		mustLinks string
	}{{1, i18n.T(ctx, i18n.KeyPrevPage), "campaign_page=2"}, {3, i18n.T(ctx, i18n.KeyNextPage), "campaign_page=2"}} {
		v := DealsView{Page: 3, PageSize: 24, Total: 100, Campaigns: Listed{Page: tt.page, PageSize: ListedPageSize, Total: 2*ListedPageSize + 1, Rows: []Summary{{Slug: "seventh", Title: "Seventh campaign"}}}}
		html := renderToString(t, Deals(DealsMeta(ctx), v))
		nav := navLabelled(t, html, campaigns)
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

// TestDealsNotCalledLimitedTime: nothing offered is called limited time: the
// deals title, page, campaign meta, strip and header never say 限時, and the
// deals title is 優惠. The nav and the department notice are held by the
// catalog package's test of the same wording.
func TestDealsNotCalledLimitedTime(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var page, list strings.Builder
	if err := Deals(DealsMeta(ctx), DealsView{}).Render(ctx, &page); err != nil {
		t.Fatalf("render deals: %v", err)
	}
	if err := strip([]Summary{{Slug: "a", Title: "秋日選物", Products: 2}}).Render(ctx, &list); err != nil {
		t.Fatalf("render strip: %v", err)
	}
	for name, text := range map[string]string{
		"deals title":      DealsMeta(ctx).Title,
		"deals page":       page.String(),
		"campaign meta":    Meta(ctx, "秋日選物", widget.Photo{}).Description,
		"campaigns strip":  list.String(),
		"campaigns header": i18n.T(ctx, i18n.KeyCampaignsRunning),
	} {
		if strings.Contains(text, "限時") {
			t.Errorf("%s says 限時", name)
		}
	}
	if got := DealsMeta(ctx).Title; got != "優惠" {
		t.Errorf("DealsMeta title = %q, want 優惠", got)
	}
}
