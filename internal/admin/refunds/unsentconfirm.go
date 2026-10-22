package refunds

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/ui/widget"
)

// UnsentRefundConfirmation is what pressing the refund of unsent units will
// do, shown before any money moves: the amounts are the ones its approval
// freezes.
type UnsentRefundConfirmation struct {
	OrderNumber string
	Lines       []UnsentLine
	Reason      returns.UnsentReason
	RefundCents int64
	CardCents   int64
	CreditCents int64
	// Resume finishes the refund already opened; its note is recorded.
	Resume      bool
	Note        string
	NoteInvalid bool
}

func (v UnsentRefundConfirmation) Amount() string { return money.TWD(v.RefundCents) }

// Total is the amount the confirming POST repeats; a different figure is a
// refund the staff member was not shown.
func (v UnsentRefundConfirmation) Total() string { return strconv.FormatInt(v.RefundCents, 10) }

func (v UnsentRefundConfirmation) Channel(ctx context.Context) string {
	return widget.PayoutChannel(ctx, v.CardCents, v.CreditCents)
}

func (v UnsentRefundConfirmation) ItemsText(ctx context.Context) string {
	return unsentLinesText(ctx, v.Lines)
}

func (v UnsentRefundConfirmation) ReasonText(ctx context.Context) string {
	return v.Reason.Label(ctx)
}

// StockText is what settling does to the units' stock: a withdrawn unit goes
// back on sale, any other is written off (settle_unsent_refund).
func (v UnsentRefundConfirmation) StockText(ctx context.Context) string {
	if v.Reason == returns.UnsentCustomerWithdrew {
		return i18n.T(ctx, i18n.KeyAdminUnsentStockBack)
	}
	return i18n.T(ctx, i18n.KeyAdminUnsentStockWrittenOff)
}

func (v UnsentRefundConfirmation) Action() string {
	return "/admin/orders/" + v.OrderNumber + "/refund-unsent"
}

func (v UnsentRefundConfirmation) Back() string { return "/admin/orders/" + v.OrderNumber }

// UnsentLine is one line of a refund of units an order will not send. The
// orders desk keeps a copy (orders.UnsentLine) for the open refund on the order
// page, which lists the same units the same way and posts the same fields here.
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

// unsentLinesText lists the lines as name × quantity, as the order page's open
// refund does.
func unsentLinesText(ctx context.Context, lines []UnsentLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.Line() + " × " + l.QuantityText()
	}
	return strings.Join(parts, i18n.T(ctx, i18n.KeyListSeparator))
}
