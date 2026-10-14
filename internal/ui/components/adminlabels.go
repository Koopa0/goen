package components

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

// ToggleLabel names the button that flips a switch: it offers the state the switch is not in.
func ToggleLabel(ctx context.Context, active bool) string {
	if active {
		return i18n.T(ctx, i18n.KeyAdminToggleOff)
	}
	return i18n.T(ctx, i18n.KeyAdminToggleOn)
}

// DollarInputType is the type of an input holding a dollar amount. A number input
// sanitizes unreadable raw values to blank. A refused draft must remain visible
// and editable, including whitespace and an overflowing amount.
func DollarInputType(raw string) string {
	if raw == "" {
		return "number"
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err != nil || raw[0] == '+' {
		return "text"
	}
	return "number"
}

// DayOf is the calendar day t falls on in its own zone.
func DayOf(t time.Time) shoptime.Date {
	return shoptime.Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// DayText is a day as a report heading writes it.
func DayText(ctx context.Context, d shoptime.Date) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepDay), d.Month.String()[:3], int(d.Month), d.Day)
}
