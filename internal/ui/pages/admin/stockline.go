package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
)

// StockLineDays is how many shop days the stock line covers, today included.
const StockLineDays = 90

// minLineMovements is the fewest movements in those days that make a line: with
// one there is a single step to draw and nothing to read about how often goods
// arrive.
const minLineMovements = 2

// StockDay is one shop day of a variant's ledger: the stock at its close, the
// units received that day and how many movements, receipts among them, it held.
type StockDay struct {
	Day      time.Time
	Stock    int32
	Received int32
	Receipts int32
	Moves    int32
}

// HasStockDays is whether the variant's days were read, which a later page of
// its ledger does not do.
func (v *MovementsView) HasStockDays() bool { return len(v.Days) > 0 }

func (v *MovementsView) movesInDays() int32 {
	var moves int32
	for _, d := range v.Days {
		moves += d.Moves
	}
	return moves
}

// DrawsStockLine is whether there are movements enough to draw the days.
func (v *MovementsView) DrawsStockLine() bool { return v.movesInDays() >= minLineMovements }

// NoStockLine says why the days are not drawn.
func (v *MovementsView) NoStockLine(ctx context.Context) string {
	key := i18n.KeyAdminStockLineNone
	if v.movesInDays() > 0 {
		key = i18n.KeyAdminStockLineOne
	}
	return i18n.Count(ctx, key, int64(len(v.Days)), len(v.Days))
}

// StockLine is the chart of the days. Its caption counts the receipts and the
// days at the safety level, each as a sentence of its own, and says how low the
// stock went when it never reached the safety level.
func (v *MovementsView) StockLine(ctx context.Context) chart.StepLineProps {
	stock := chart.Series{Label: i18n.T(ctx, i18n.KeyAdminStockLineStock), Buckets: make([]chart.Bucket, len(v.Days))}
	received := chart.Series{Label: i18n.T(ctx, i18n.KeyAdminStockLineReceived), Buckets: make([]chart.Bucket, len(v.Days))}
	var receipts, held int64
	lowest := int64(v.Stock)
	for i, d := range v.Days {
		stock.Buckets[i] = chart.Bucket{Day: d.Day, Value: int64(d.Stock)}
		received.Buckets[i] = chart.Bucket{Day: d.Day, Value: int64(d.Received)}
		receipts += int64(d.Receipts)
		lowest = min(lowest, int64(d.Stock))
		if v.Safety > 0 && d.Stock <= v.Safety {
			held++
		}
	}

	days := len(v.Days)
	atSafety := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockLineLow), lowest)
	if held > 0 {
		atSafety = i18n.Count(ctx, i18n.KeyAdminStockLineHeld, held, held)
	}
	return chart.StepLineProps{
		Stock: stock, Received: received, Safety: int64(v.Safety),
		SafetyHeading: i18n.T(ctx, i18n.KeyAdminStockLineSafety),
		DayHeading:    i18n.T(ctx, i18n.KeyAdminRepDate),
		Caption: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockLineCaption),
			fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminStockLineNow), v.Stock),
			i18n.Count(ctx, i18n.KeyAdminStockLineReceipts, receipts, receipts, days),
			atSafety),
		Note: i18n.T(ctx, i18n.KeyAdminStockLineNote),
	}
}
