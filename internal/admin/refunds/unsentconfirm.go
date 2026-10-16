package refunds

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// UnsentRefundConfirmation is what pressing the refund of unsent units will
// do, shown before any money moves: the amounts are the ones its approval
// freezes.
type UnsentRefundConfirmation struct {
	OrderNumber string
	Lines       []admin.UnsentLine
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
	return (&admin.Return{CardRefundCents: v.CardCents, CreditRefundCents: v.CreditCents}).PayoutChannel(ctx)
}

func (v UnsentRefundConfirmation) ItemsText(ctx context.Context) string {
	return admin.UnsentLinesText(ctx, v.Lines)
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
