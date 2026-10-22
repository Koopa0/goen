package campaign

import (
	"context"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Schedule is what a campaign page says about its window: the fact line
// and, when the span fits one, the period's track. NewSchedule
// lays it out for the state the campaign is in.
type Schedule struct {
	Facts []widget.Stat
	// Ends and DaysLeft are Facts' own stats; DaysLeft has no value in the last two days and outside the window.
	Ends, DaysLeft widget.Stat
	Period         widget.PeriodSpec
	HasPeriod      bool
}

// CardFacts are what is left, when it is shown, and then when it ends: the
// facts of a card whose item count is its link.
func (s *Schedule) CardFacts() []widget.Stat {
	if s.DaysLeft.Label == "" {
		return []widget.Stat{s.Ends}
	}
	return []widget.Stat{s.DaysLeft, s.Ends}
}

// NewSchedule states a campaign of items products running from startsAt
// to endsAt, which is exclusive, as it stands at now.
func NewSchedule(ctx context.Context, title string, items int64, startsAt, endsAt, now time.Time) Schedule {
	state := StateAt(startsAt, endsAt, now)
	count := widget.Stat{Label: i18n.T(ctx, i18n.KeySlideItems), Value: widget.StatCount(items, i18n.T(ctx, i18n.KeyFactUnitItems))}
	ends := EndStat(ctx, endsAt, now)
	schedule := Schedule{Ends: ends}

	switch state {
	case NotStarted:
		first := shoptime.DateOf(startsAt, now)
		starts := widget.Stat{
			Label: i18n.T(ctx, i18n.KeyCampaignStarts),
			Value: widget.StatDate(shoptime.DateText(ctx, first), "").WithDatetime(first.ISO()),
		}
		schedule.Ends.Note = ""
		schedule.Facts = []widget.Stat{starts, schedule.Ends, count}
	case Ended:
		schedule.Ends.Note = i18n.T(ctx, i18n.KeyCampaignEnded)
		schedule.Facts = []widget.Stat{schedule.Ends, count}
	default:
		// The last two days say so in the end's note instead.
		if left := shoptime.DaysLeft(now, endsAt); left > 1 {
			schedule.DaysLeft = widget.Stat{Label: i18n.T(ctx, i18n.KeySlideDaysLeft), Value: widget.StatCount(int64(left), i18n.T(ctx, i18n.KeyFactUnitDays))}
		}
		schedule.Facts = []widget.Stat{count, schedule.Ends}
		if schedule.DaysLeft.Label != "" {
			schedule.Facts = append(schedule.Facts, schedule.DaysLeft)
		}
	}
	schedule.Period, schedule.HasPeriod = widget.DayPeriod(ctx, title, startsAt, endsAt, now)
	return schedule
}
