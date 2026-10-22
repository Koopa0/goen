package widget

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
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
