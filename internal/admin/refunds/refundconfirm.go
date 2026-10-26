package refunds

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/payment/refund"
)

// Confirmation is a refund before shipment, before it moves money.
// Resume means the refund is already open, its split frozen and its reason
// recorded. CreditPaid is a pending order store credit alone paid: confirming
// cancels it at once and returns the credit, as its customer's own
// cancellation would.
type Confirmation struct {
	OrderNumber   string
	TotalCents    int64
	CardCents     int64
	CreditCents   int64
	Resume        bool
	CreditPaid    bool
	Reason        string
	ReasonInvalid bool
}

func (v Confirmation) Amount() string { return money.TWD(v.TotalCents) }

// Total is the amount the confirming POST repeats; a different figure is a
// refund the staff member was not shown.
func (v Confirmation) Total() string { return strconv.FormatInt(v.TotalCents, 10) }

// CreditCancellation is what confirming does to an order store credit alone
// paid.
func (v Confirmation) CreditCancellation(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRefundCreditReturn), money.TWD(v.CreditCents))
}

func (v Confirmation) Channel(ctx context.Context) string {
	return refund.PayoutChannel(ctx, v.CardCents, v.CreditCents)
}

func (v Confirmation) Action() string { return "/admin/orders/" + v.OrderNumber + "/refund" }

func (v Confirmation) Back() string { return "/admin/orders/" + v.OrderNumber }
