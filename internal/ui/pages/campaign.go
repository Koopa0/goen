package pages

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// CampaignEndsOn is "" while the last day is more than 30 days off: a year-long
// "limited time" date reads as a deadline the shop does not mean.
func CampaignEndsOn(ctx context.Context, endsAt, now time.Time) string {
	if endsAt.After(now.AddDate(0, 0, 30)) {
		return ""
	}
	return shoptime.DateText(ctx, shoptime.DateOf(endsAt, now))
}

type CampaignSummary struct {
	Slug     string
	Title    string
	Products int64
	EndsOn   string
}

func (c CampaignSummary) Href() string { return "/s/" + c.Slug }

func (c CampaignSummary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

type CampaignView struct {
	Slug     string
	Title    string
	EndsOn   string
	Products []ProductTile
	Image    Photo
	Tone     Tone
}

func (v CampaignView) Empty() bool { return len(v.Products) == 0 }

func CampaignMeta(ctx context.Context, title string, photo Photo) layouts.Page {
	return layouts.Page{
		Title:       title,
		Description: fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignDescription), title),
		Share:       photo.share(title),
	}
}

// CampaignPage keeps the promotion pager independent of the discounted-product pager.
type CampaignPage struct {
	Rows     []CampaignSummary
	Page     int
	Total    int64
	PageSize int
}

func (p CampaignPage) Pages() int {
	if p.PageSize <= 0 {
		return 1
	}
	return max(1, int((p.Total+int64(p.PageSize)-1)/int64(p.PageSize)))
}
