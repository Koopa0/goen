package campaign

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Summary is a campaign as the strip on /deals lists it.
type Summary struct {
	Slug     string
	Title    string
	Products int64
	EndsOn   string
}

func (c Summary) Href() string { return "/s/" + c.Slug }

func (c Summary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// View is a campaign as its page at /s/{slug} shows it.
type View struct {
	Slug     string
	Title    string
	Schedule Schedule
	Products []tile.Tile
	Image    widget.Photo
	Tone     tone.Tone
}

func (v *View) Empty() bool { return len(v.Products) == 0 }

func Meta(ctx context.Context, title string, photo widget.Photo) frame.Page {
	return frame.Page{
		Title:       title,
		Description: fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignDescription), title),
		Share:       frame.NewShareImage(photo, title),
	}
}

// Listed keeps the promotion pager independent of the discounted-product pager.
type Listed struct {
	Rows     []Summary
	Page     int
	Total    int64
	PageSize int
}

func (p Listed) Pages() int {
	if p.PageSize <= 0 {
		return 1
	}
	return max(1, int((p.Total+int64(p.PageSize)-1)/int64(p.PageSize)))
}

// DealsView is /deals: one page of the discounted products and one page of
// the campaigns listed above them, each paged on its own.
type DealsView struct {
	Products  []tile.Tile
	Total     int64
	Page      int
	PageSize  int
	Campaigns Listed
}

func DealsMeta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyDealsTitle)}
}

func (v DealsView) TotalText() string { return strconv.FormatInt(v.Total, 10) }

func (v DealsView) Pages() int {
	if v.PageSize <= 0 || v.Total <= 0 {
		return 1
	}
	n := int((v.Total + int64(v.PageSize) - 1) / int64(v.PageSize))
	return max(n, 1)
}

func (v DealsView) HasPrev() bool { return v.Page > 1 }
func (v DealsView) HasNext() bool { return v.Page < v.Pages() }

func (v DealsView) PrevHref() string { return v.PageHref(v.Page - 1) }
func (v DealsView) NextHref() string { return v.PageHref(v.Page + 1) }

// PageHref keeps the campaign strip's page while the products page.
func (v DealsView) PageHref(n int) string {
	q := url.Values{}
	if n > 1 {
		q.Set("page", strconv.Itoa(n))
	}
	if v.Campaigns.Page > 1 {
		q.Set("campaign_page", strconv.Itoa(v.Campaigns.Page))
	}
	if len(q) > 0 {
		return "/deals?" + q.Encode()
	}
	return "/deals"
}

func (v DealsView) PageText() string  { return strconv.Itoa(v.Page) }
func (v DealsView) PagesText() string { return strconv.Itoa(v.Pages()) }

func (v DealsView) HasCampaigns() bool { return len(v.Campaigns.Rows) > 0 }

// CampaignPageHref keeps the products' page while the campaign strip pages.
func (v DealsView) CampaignPageHref(page int) string {
	q := url.Values{}
	if v.Page > 1 {
		q.Set("page", strconv.Itoa(v.Page))
	}
	if page > 1 {
		q.Set("campaign_page", strconv.Itoa(page))
	}
	href := "/deals"
	if len(q) > 0 {
		href += "?" + q.Encode()
	}
	return href + "#campaigns"
}
