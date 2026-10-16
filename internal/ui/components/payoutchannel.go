package components

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

// PayoutChannel names a refund's sources from its card and store-credit
// halves, or "" when both are zero: the returns queue and the refunds desk's
// confirmations name the channel this way.
func PayoutChannel(ctx context.Context, cardCents, creditCents int64) string {
	switch {
	case cardCents > 0 && creditCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutSplit),
			money.TWD(cardCents), money.TWD(creditCents))
	case creditCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCredit), money.TWD(creditCents))
	case cardCents > 0:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRetPayoutCard), money.TWD(cardCents))
	default:
		return ""
	}
}
