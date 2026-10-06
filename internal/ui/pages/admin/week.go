package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/components"
)

// WeekDays is how many shop days the dashboard's figures cover.
const WeekDays = 7

// Week is the last WeekDays shop days of paid orders and revenue, read as the
// report reads them, and the same number of days before them.
type Week struct {
	Orders         int64
	RevenueCents   int64
	RevenueSquares float64
	Previous       PreviousFigures
	RevenueDays    chart.SparklineProps
	OrderDays      chart.SparklineProps
}

// LatestPaid is the newest order whose money has come in, and how long ago.
type LatestPaid struct {
	Number     string
	TotalCents int64
	Elapsed    time.Duration
}

func (w *Week) revenueAgainst(ctx context.Context) string {
	return against(ctx, WeekDays, comparison{
		Orders: w.Orders, PreviousOrders: w.Previous.Orders,
		Current: w.RevenueCents, Previous: w.Previous.RevenueCents,
		Noise:        w.RevenueSquares + w.Previous.RevenueSquares,
		PreviousText: money.TWD(w.Previous.RevenueCents),
	})
}

func (w *Week) ordersAgainst(ctx context.Context) string {
	return against(ctx, WeekDays, comparison{
		Orders: w.Orders, PreviousOrders: w.Previous.Orders,
		Current: w.Orders, Previous: w.Previous.Orders,
		Noise:        float64(w.Orders + w.Previous.Orders),
		PreviousText: strconv.FormatInt(w.Previous.Orders, 10),
	})
}

// sparklines labels the keys under the two small charts.
func sparklines(ctx context.Context, p chart.SparklineProps) chart.SparklineProps {
	p.Previous.Label = i18n.Count(ctx, i18n.KeyAdminRepPreviousDays, WeekDays, WeekDays)
	p.Current.Label = i18n.Count(ctx, i18n.KeyAdminRepLastDays, WeekDays, WeekDays)
	return p
}

// WeekStats is the stat line of the last week: revenue and paid orders each set
// against the week before with their days drawn, then the latest paid order.
func (v *DashboardView) WeekStats(ctx context.Context) []components.LinkedStat {
	const reports = "/admin/reports?days=7"
	if v.WeekUnavailable {
		return nil
	}
	w := &v.Week
	return []components.LinkedStat{
		{Stat: components.Stat{
			Label: i18n.T(ctx, i18n.KeyAdminRepRevenue), Value: components.StatMoney(w.RevenueCents),
			Note: w.revenueAgainst(ctx), Trend: chart.Sparkline(sparklines(ctx, w.RevenueDays)),
		}, Href: reports},
		{Stat: components.Stat{
			Label: i18n.T(ctx, i18n.KeyAdminRepPaidOrders), Value: components.StatNumber(w.Orders),
			Note: w.ordersAgainst(ctx), Trend: chart.Sparkline(sparklines(ctx, w.OrderDays)),
		}, Href: reports},
		v.latestStat(ctx),
	}
}

func (v *DashboardView) latestStat(ctx context.Context) components.LinkedStat {
	stat := components.LinkedStat{
		Stat: components.Stat{Label: i18n.T(ctx, i18n.KeyAdminQueueLatestPaid), Value: components.StatWord("—")},
		Href: "/admin/orders",
	}
	switch {
	case v.LatestUnavailable:
		stat.Note = i18n.T(ctx, i18n.KeyAdminQueueLatestUnavailable)
	case v.Latest == nil:
		stat.Note = i18n.T(ctx, i18n.KeyAdminQueueLatestNone)
	default:
		stat.Value = elapsedValue(ctx, v.Latest.Elapsed)
		stat.Note = v.Latest.Number + " · " + money.TWD(v.Latest.TotalCents)
		stat.Href = "/admin/orders/" + v.Latest.Number
	}
	return stat
}

// elapsedValue is how long ago something was, in its largest whole unit, with
// the number as the figure and the rest as its unit.
func elapsedValue(ctx context.Context, d time.Duration) components.StatValue {
	var key i18n.Key
	var n int64
	switch {
	case d < time.Minute:
		return components.StatWord(i18n.T(ctx, i18n.KeyAdminQueueAgoNow))
	case d < time.Hour:
		key, n = i18n.KeyAdminQueueAgoMinutes, int64(d/time.Minute)
	case d < 24*time.Hour:
		key, n = i18n.KeyAdminQueueAgoHours, int64(d/time.Hour)
	default:
		key, n = i18n.KeyAdminQueueAgoDays, int64(d/(24*time.Hour))
	}
	figure := strconv.FormatInt(n, 10)
	return components.StatCount(n, strings.TrimSpace(strings.TrimPrefix(i18n.Count(ctx, key, n, n), figure)))
}
