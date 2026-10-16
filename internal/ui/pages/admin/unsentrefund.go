package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// UnsentLine is one line of a refund of units an order will not send.
type UnsentLine struct {
	OrderLineID string
	Name        string
	Label       string
	Quantity    int32
}

func (l UnsentLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name
}

func (l UnsentLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// Field is the form field that names this line's quantity, the one the
// dispatch form uses for the same line.
func (l UnsentLine) Field() string { return "qty_" + l.OrderLineID }

// UnsentLinesText lists the lines as name × quantity: the order page's open
// refund and the refunds desk's confirmation show the units this way.
func UnsentLinesText(ctx context.Context, lines []UnsentLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.Line() + " × " + l.QuantityText()
	}
	return strings.Join(parts, i18n.T(ctx, i18n.KeyListSeparator))
}

// UnsentOpenText says which units the unfinished refund covers.
func (v *OrderView) UnsentOpenText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminUnsentOpen), UnsentLinesText(ctx, v.UnsentOpenLines))
}
