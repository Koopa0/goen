package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// CampaignSummary is a running promotion, as a page that lists them shows it.
type CampaignSummary struct {
	Slug     string
	Title    string
	Products int64
	EndsAt   string
	// EndsIn is roughly how long is left, computed server-side: a precise
	// countdown that only moves on reload is worse than none.
	EndsIn string
}

// Href is the campaign's page.
func (c CampaignSummary) Href() string { return "/s/" + c.Slug }

// ProductsText is how many things it features.
func (c CampaignSummary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// CampaignView is one promotion and what it features.
type CampaignView struct {
	Slug     string
	Title    string
	EndsAt   string
	Products []ProductTile
}

// Empty reports whether the promotion features nothing that is still for sale.
func (v CampaignView) Empty() bool { return len(v.Products) == 0 }

// CampaignMeta is the chrome view model for a campaign page.
func CampaignMeta(ctx context.Context, title string) layouts.Page {
	return layouts.Page{
		Title:       title,
		Description: fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignDescription), title),
	}
}
