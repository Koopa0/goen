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

// CampaignEndsOn is a running promotion's last day in the reader's language, or
// "" while that day is more than 30 days off: a year-long "limited time" date
// reads as a deadline the shop does not mean.
func CampaignEndsOn(ctx context.Context, endsAt, now time.Time) string {
	if endsAt.After(now.AddDate(0, 0, 30)) {
		return ""
	}
	d := shoptime.DateOf(endsAt, now)
	key := i18n.KeyShortDate
	if d.OtherYear {
		key = i18n.KeyShortDateYear
	}
	return fmt.Sprintf(i18n.T(ctx, key), d.Month.String()[:3], int(d.Month), d.Day, d.Year)
}

// CampaignSummary is a running promotion, as a page that lists them shows it.
type CampaignSummary struct {
	Slug     string
	Title    string
	Products int64
	EndsOn   string
}

// Href is the campaign's page.
func (c CampaignSummary) Href() string { return "/s/" + c.Slug }

// ProductsText is how many things it features.
func (c CampaignSummary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// CampaignView is one promotion and what it features.
type CampaignView struct {
	Slug     string
	Title    string
	EndsOn   string
	Products []ProductTile
	Image    Photo
	Tone     Tone
}

// Empty reports whether the promotion features nothing that is still for sale.
func (v CampaignView) Empty() bool { return len(v.Products) == 0 }

// CampaignMeta is the chrome view model for a campaign page.
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
