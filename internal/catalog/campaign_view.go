package catalog

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

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
	Schedule campaign.Schedule
	Products []pages.ProductTile
	Image    components.Photo
	Tone     components.Tone
}

func (v *CampaignView) Empty() bool { return len(v.Products) == 0 }

func CampaignMeta(ctx context.Context, title string, photo components.Photo) layouts.Page {
	return layouts.Page{
		Title:       title,
		Description: fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignDescription), title),
		Share:       shareImage(photo, title),
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
