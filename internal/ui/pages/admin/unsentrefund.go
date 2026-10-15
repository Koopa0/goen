package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/returns"
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

func unsentLinesText(ctx context.Context, lines []UnsentLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.Line() + " × " + l.QuantityText()
	}
	return strings.Join(parts, i18n.T(ctx, i18n.KeyListSeparator))
}

// UnsentReasonLabel is the staff member's code as the back office words it.
func UnsentReasonLabel(ctx context.Context, r returns.UnsentReason) string {
	switch r {
	case returns.UnsentDamaged:
		return i18n.T(ctx, i18n.KeyUnsentReasonDamaged)
	case returns.UnsentLost:
		return i18n.T(ctx, i18n.KeyUnsentReasonLost)
	case returns.UnsentSupplierFailed:
		return i18n.T(ctx, i18n.KeyUnsentReasonSupplierFailed)
	case returns.UnsentCustomerWithdrew:
		return i18n.T(ctx, i18n.KeyUnsentReasonCustomerWithdrew)
	default:
		return string(r)
	}
}

// UnsentOpenText says which units the unfinished refund covers.
func (v *OrderView) UnsentOpenText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminUnsentOpen), unsentLinesText(ctx, v.UnsentOpenLines))
}

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
	return (&Return{CardRefundCents: v.CardCents, CreditRefundCents: v.CreditCents}).PayoutChannel(ctx)
}

func (v UnsentRefundConfirmation) ItemsText(ctx context.Context) string {
	return unsentLinesText(ctx, v.Lines)
}

func (v UnsentRefundConfirmation) ReasonText(ctx context.Context) string {
	return UnsentReasonLabel(ctx, v.Reason)
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
