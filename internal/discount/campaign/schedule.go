package campaign

import (
	"context"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Schedule is what a campaign page says about its window: for a running
// campaign the phrase of its end, for one not started or ended the fact line,
// and, when the span fits one, the period's track. NewSchedule lays it out for
// the state the campaign is in.
type Schedule struct {
	// End is set only while the campaign runs.
	End   *End
	Facts []widget.Stat
	// Ends is one of Facts' own stats.
	Ends      widget.Stat
	Period    widget.PeriodSpec
	HasPeriod bool
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
		end := NewEnd(endsAt, now)
		schedule.End = &end
	}
	schedule.Period, schedule.HasPeriod = widget.DayPeriod(ctx, title, startsAt, endsAt, now)
	return schedule
}
