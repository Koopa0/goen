package widget

import (
	"context"

	"github.com/koopa0/goen/internal/shoptime"
)

// DateStat is a day as a stat, with an optional note under it.
func DateStat(ctx context.Context, label string, d shoptime.Date, note string) Stat {
	return Stat{
		Label: label,
		Value: StatDate(shoptime.DateText(ctx, d), "").WithDatetime(d.ISO()),
		Note:  note,
	}
}
