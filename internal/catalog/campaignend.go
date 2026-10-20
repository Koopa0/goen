package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
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
