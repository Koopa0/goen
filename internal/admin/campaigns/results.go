package campaigns

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// maxResultDays is how many of a campaign's first days its results show, and
// as many days before it.
const maxResultDays = 14

// resultDays are the whole shop days a campaign's results cover: days from
// the day before its start, and days from its first day on.
type resultDays struct {
	days     int
	first    time.Time // the first day before the campaign, as a UTC date
	last     time.Time // the last day of the campaign counted, as a UTC date
	from, to time.Time // the instants those days run between; to is no later than now
	partial  bool      // the last day is today, still going
}

// resultWindow is the days the results of a campaign with these dates cover at
// now. It is false until the campaign's first day has come.
func resultWindow(starts, ends, now time.Time) (resultDays, bool) {
	begin := shoptime.Midnight(starts)
	if begin.After(now) {
		return resultDays{}, false
	}
	// A campaign ends on the day its last instant falls on: an ends_at at
	// midnight belongs to the day before.
	through := shoptime.Midnight(ends.Add(-time.Nanosecond))
	if today := shoptime.Midnight(now); through.After(today) {
		through = today
	}
	days := min(shoptime.DayUTC(through).Sub(shoptime.DayUTC(begin)).Hours()/24+1, maxResultDays)
	n := int(days)
	end := begin.AddDate(0, 0, n)
	w := resultDays{
		days:  n,
		first: shoptime.DayUTC(begin.AddDate(0, 0, -n)),
		last:  shoptime.DayUTC(begin.AddDate(0, 0, n-1)),
		from:  begin.AddDate(0, 0, -n),
		to:    end,
	}
	if now.Before(end) {
		w.to, w.partial = now, true
	}
	return w, true
}

// Results reads the units sold on each shop day by the products now on the
// campaign's list: the days of the campaign so far, up to 14, and as many
// before it. It is nil when the campaign has not begun or has no products.
func (s *Store) Results(ctx context.Context, slug string, detail admin.CampaignDetail, products int, now time.Time) (*admin.CampaignResults, error) {
	w, ok := resultWindow(detail.Starts, detail.Ends, now)
	if !ok || products == 0 {
		return nil, nil
	}
	rows, err := s.q.CampaignDailyUnits(ctx, db.CampaignDailyUnitsParams{
		Slug: slug, FirstDay: w.first, LastDay: w.last, FromAt: w.from, ToAt: w.to,
	})
	if err != nil {
		return nil, fmt.Errorf("read campaign daily units: %w", err)
	}
	units := chart.Series{Partial: w.partial, Buckets: make([]chart.Bucket, 0, len(rows))}
	for _, r := range rows {
		units.Buckets = append(units.Buckets, chart.Bucket{Day: r.Day, Value: r.Units})
	}
	return &admin.CampaignResults{
		Units: units, Days: w.days, Products: products, Cut: shoptime.Clock(now),
		Campaign: chart.Span{
			From:  shoptime.DayUTC(detail.Starts),
			To:    shoptime.DayUTC(detail.Ends.Add(-time.Nanosecond)),
			Label: detail.Title,
		},
	}, nil
}
