package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type CampaignSummary struct {
	Slug     string
	Title    string
	Products int64
	EndsOn   string
}

func (c CampaignSummary) Href() string { return "/s/" + c.Slug }

func (c CampaignSummary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// CampaignSchedule is what a campaign page says about its window: the fact line
// and, when the span fits one, the period's track. catalog.NewCampaignSchedule
// lays it out for the state the campaign is in.
type CampaignSchedule struct {
	Facts []components.Stat
	// Ends and DaysLeft are Facts' own stats; DaysLeft has no value in the last two days and outside the window.
	Ends, DaysLeft components.Stat
	Period         components.PeriodSpec
	HasPeriod      bool
}

// CardFacts are what is left, when it is shown, and then when it ends: the
// facts of a card whose item count is its link.
func (s *CampaignSchedule) CardFacts() []components.Stat {
	if s.DaysLeft.Label == "" {
		return []components.Stat{s.Ends}
	}
	return []components.Stat{s.DaysLeft, s.Ends}
}

type CampaignView struct {
	Slug     string
	Title    string
	Schedule CampaignSchedule
	Products []ProductTile
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
