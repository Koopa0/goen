package pages

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// CampaignEndsOn is the last day a campaign runs, said the short way.
func CampaignEndsOn(ctx context.Context, endsAt, now time.Time) string {
	return shoptime.DateText(ctx, shoptime.LastDay(endsAt, now))
}

type CampaignSummary struct {
	Slug     string
	Title    string
	Products int64
	EndsOn   string
}

func (c CampaignSummary) Href() string { return "/s/" + c.Slug }

func (c CampaignSummary) ProductsText() string { return strconv.FormatInt(c.Products, 10) }

// CampaignState is where a campaign's window lies against the present; the zero value is running.
type CampaignState string

const (
	CampaignRunning    CampaignState = ""
	CampaignNotStarted CampaignState = "not-started"
	CampaignEnded      CampaignState = "ended"
)

// CampaignStateAt judges the window [startsAt, endsAt) at now.
func CampaignStateAt(startsAt, endsAt, now time.Time) CampaignState {
	switch {
	case now.Before(startsAt):
		return CampaignNotStarted
	case !now.Before(endsAt):
		return CampaignEnded
	}
	return CampaignRunning
}

// CampaignSchedule is what a campaign page says about its window: the fact line
// and, when the span fits one, the day grid.
type CampaignSchedule struct {
	State     CampaignState
	Facts     []components.Stat
	Period    components.PeriodSpec
	HasPeriod bool
}

// NewCampaignSchedule states a campaign of items products running from startsAt
// to endsAt, which is exclusive.
func NewCampaignSchedule(ctx context.Context, title string, items int64, startsAt, endsAt, now time.Time) CampaignSchedule {
	state := CampaignStateAt(startsAt, endsAt, now)
	count := components.Stat{Label: i18n.T(ctx, i18n.KeySlideItems), Value: statCount(ctx, i18n.KeyUnitItems, items)}

	clock := ""
	if !shoptime.Midnight(endsAt).Equal(endsAt) {
		clock = shoptime.ClockText(endsAt)
	}
	lastDay := shoptime.LastDay(endsAt, now)
	datetime := lastDay.ISO()
	if clock != "" {
		datetime += "T" + clock
	}
	ends := components.Stat{
		Label: i18n.T(ctx, i18n.KeySlideEnds),
		Value: components.StatDate(shoptime.DateText(ctx, lastDay), clock).WithDatetime(datetime),
	}

	var facts []components.Stat
	switch state {
	case CampaignNotStarted:
		first := shoptime.DateOf(startsAt, now)
		starts := components.Stat{
			Label: i18n.T(ctx, i18n.KeyCampaignStarts),
			Value: components.StatDate(shoptime.DateText(ctx, first), "").WithDatetime(first.ISO()),
		}
		facts = []components.Stat{starts, ends, count}
	case CampaignEnded:
		ends.Note = i18n.T(ctx, i18n.KeyCampaignEnded)
		facts = []components.Stat{ends, count}
	default:
		facts = []components.Stat{count, ends}
		switch left := shoptime.DaysLeft(now, endsAt); {
		case left <= 0:
			facts[1].Note = i18n.T(ctx, i18n.KeyEndsToday)
		case left == 1:
			facts[1].Note = i18n.T(ctx, i18n.KeyEndsTomorrow)
		default:
			facts = append(facts, components.Stat{Label: i18n.T(ctx, i18n.KeySlideDaysLeft), Value: statCount(ctx, i18n.KeyUnitDays, int64(left))})
		}
	}
	period, ok := components.DayPeriod(ctx, title, startsAt, endsAt, now)
	return CampaignSchedule{State: state, Facts: facts, Period: period, HasPeriod: ok}
}

// statCount is n with the unit its key says, which follows the number after a no-break space.
func statCount(ctx context.Context, k i18n.Key, n int64) components.StatValue {
	_, unit, _ := strings.Cut(i18n.Count(ctx, k, n, n), "\u00a0")
	return components.StatCount(n, unit)
}

type CampaignView struct {
	Slug     string
	Title    string
	Schedule CampaignSchedule
	Products []ProductTile
	Image    Photo
	Tone     Tone
}

// Tiles are the campaign's products; outside its window a price is not struck,
// because no campaign is running to have lowered it.
func (v CampaignView) Tiles() []ProductTile {
	if v.Schedule.State == CampaignRunning {
		return v.Products
	}
	out := slices.Clone(v.Products)
	for i := range out {
		out[i].CompareCents = 0
	}
	return out
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
