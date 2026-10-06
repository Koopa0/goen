package shoptime

import (
	"context"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// DateText is d as the shopper reads it: 10 月 30 日 or Oct 30, with the year
// when d is outside the shop's current one.
func DateText(ctx context.Context, d Date) string {
	key := i18n.KeyShortDate
	if d.OtherYear {
		key = i18n.KeyShortDateYear
	}
	return fmt.Sprintf(i18n.T(ctx, key), d.Month.String()[:3], int(d.Month), d.Day, d.Year)
}

// DateTimeText is DateText followed by the shop's clock time of t.
func DateTimeText(ctx context.Context, t, now time.Time) string {
	return DateText(ctx, DateOf(t, now)) + " " + In(t).Format("15:04")
}
