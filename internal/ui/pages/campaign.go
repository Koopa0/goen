package pages

import (
	"context"
	"fmt"
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

// endClock is the shop's time of day a campaign ends, or "" when it ends at midnight.
func endClock(endsAt time.Time) string {
	if shoptime.Midnight(endsAt).Equal(endsAt) {
		return ""
	}
	return shoptime.ClockText(endsAt)
}

// CampaignEndStat is the campaign's last day as a stat, with the time of day when it does not end at
// midnight; the last two days say so in its note.
func CampaignEndStat(ctx context.Context, endsAt, now time.Time) components.Stat {
	clock := endClock(endsAt)
	datetime := shoptime.LastDay(endsAt, now).ISO()
	if clock != "" {
		datetime += "T" + clock
	}
	stat := components.Stat{
		Label: i18n.T(ctx, i18n.KeySlideEnds),
		Value: components.StatDate(CampaignEndsOn(ctx, endsAt, now), clock).WithDatetime(datetime),
	}
	switch left := shoptime.DaysLeft(now, endsAt); {
	case left <= 0:
		stat.Note = i18n.T(ctx, i18n.KeyEndsToday)
	case left == 1:
		stat.Note = i18n.T(ctx, i18n.KeyEndsTomorrow)
	}
	return stat
}

// CampaignEnd is when a running campaign ends, for a sentence rather than a fact line: its last day, and the
// clock time where it does not end at midnight.
type CampaignEnd struct {
	Last     shoptime.Date
	Clock    string
	DaysLeft int
}

func NewCampaignEnd(endsAt, now time.Time) CampaignEnd {
	return CampaignEnd{Last: shoptime.LastDay(endsAt, now), Clock: endClock(endsAt), DaysLeft: shoptime.DaysLeft(now, endsAt)}
}

// Day is the last day, with the time of day after it when the campaign does not end at midnight.
func (e CampaignEnd) Day(ctx context.Context) string {
	if e.Clock == "" {
		return shoptime.DateText(ctx, e.Last)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignDayAt), shoptime.DateText(ctx, e.Last), e.Clock)
}

// EndsByTomorrow reports that the campaign ends today or tomorrow, which Left then says whole, with the time
// of day where it is not midnight.
func (e CampaignEnd) EndsByTomorrow() bool { return e.DaysLeft <= 1 }

// Datetime is Day as a time element's datetime reads it.
func (e CampaignEnd) Datetime() string {
	if e.Clock == "" {
		return e.Last.ISO()
	}
	return e.Last.ISO() + "T" + e.Clock
}

// Until splits the phrase naming the last day around the date, which the page marks as a time.
func (e CampaignEnd) Until(ctx context.Context) (before, after string) {
	const mark = "\x00"
	before, after, _ = strings.Cut(fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignNoticeUntil), mark), mark)
	return before, after
}

// Left is what remains of the campaign: the days while two or more are left, then that it ends tomorrow or
// today, with the time of day when that is not midnight.
func (e CampaignEnd) Left(ctx context.Context) string {
	switch {
	case e.DaysLeft > 1:
		return i18n.Count(ctx, i18n.KeyCampaignDaysLeft, int64(e.DaysLeft), e.DaysLeft)
	case e.DaysLeft == 1 && e.Clock != "":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignEndsTomorrowAt), e.Clock)
	case e.DaysLeft == 1:
		return i18n.T(ctx, i18n.KeyCampaignEndsTomorrow)
	case e.Clock != "":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCampaignEndsTodayAt), e.Clock)
	default:
		return i18n.T(ctx, i18n.KeyCampaignEndsToday)
	}
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
// and, when the span fits one, the period's track.
type CampaignSchedule struct {
	State CampaignState
	Facts []components.Stat
	// Ends and DaysLeft are Facts' own stats; DaysLeft has no value in the last two days and outside the window.
	Ends, DaysLeft components.Stat
	Period         components.PeriodSpec
	HasPeriod      bool
}

// NewCampaignSchedule states a campaign of items products running from startsAt
// to endsAt, which is exclusive.
func NewCampaignSchedule(ctx context.Context, title string, items int64, startsAt, endsAt, now time.Time) CampaignSchedule {
	state := CampaignStateAt(startsAt, endsAt, now)
	count := components.Stat{Label: i18n.T(ctx, i18n.KeySlideItems), Value: components.StatCount(items, i18n.T(ctx, i18n.KeyFactUnitItems))}
	ends := CampaignEndStat(ctx, endsAt, now)
	schedule := CampaignSchedule{State: state, Ends: ends}

	switch state {
	case CampaignNotStarted:
		first := shoptime.DateOf(startsAt, now)
		starts := components.Stat{
			Label: i18n.T(ctx, i18n.KeyCampaignStarts),
			Value: components.StatDate(shoptime.DateText(ctx, first), "").WithDatetime(first.ISO()),
		}
		schedule.Ends.Note = ""
		schedule.Facts = []components.Stat{starts, schedule.Ends, count}
	case CampaignEnded:
		schedule.Ends.Note = i18n.T(ctx, i18n.KeyCampaignEnded)
		schedule.Facts = []components.Stat{schedule.Ends, count}
	default:
		// The last two days say so in the end's note instead.
		if left := shoptime.DaysLeft(now, endsAt); left > 1 {
			schedule.DaysLeft = components.Stat{Label: i18n.T(ctx, i18n.KeySlideDaysLeft), Value: components.StatCount(int64(left), i18n.T(ctx, i18n.KeyFactUnitDays))}
		}
		schedule.Facts = []components.Stat{count, schedule.Ends}
		if schedule.DaysLeft.Label != "" {
			schedule.Facts = append(schedule.Facts, schedule.DaysLeft)
		}
	}
	schedule.Period, schedule.HasPeriod = components.DayPeriod(ctx, title, startsAt, endsAt, now)
	return schedule
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
	Image    Photo
	Tone     Tone
}

func (v *CampaignView) Empty() bool { return len(v.Products) == 0 }

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
