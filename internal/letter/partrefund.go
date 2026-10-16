package letter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/outbox"
)

// SendOrderPartRefunded tells the customer which units will not come and how
// their money went back.
func (n Notifier) SendOrderPartRefunded(ctx context.Context, _ *outbox.OrderPartRefunded, l *notice.PartRefund) error {
	if !email.Valid(l.Address) {
		return errors.New("an order.part_refunded message has no usable recipient")
	}
	if len(l.Items) == 0 {
		return fmt.Errorf("the refund of order %s names no items", l.OrderNumber)
	}
	ctx = n.locale(ctx, l.Locale)
	items := make([]string, len(l.Items))
	for i, it := range l.Items {
		name := it.Name
		if it.Label != "" {
			name += " · " + it.Label
		}
		items[i] = name + " × " + strconv.FormatInt(int64(it.Quantity), 10)
	}
	var refunded string
	switch {
	case l.CardCents > 0 && l.CreditCents > 0:
		refunded = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailOrderPartRefundedCardAndCredit),
			twd(l.RefundCents), twd(l.CardCents), twd(l.CreditCents))
	case l.CardCents > 0:
		refunded = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailOrderPartRefundedCard), twd(l.RefundCents), twd(l.CardCents))
	case l.CreditCents > 0:
		refunded = fmt.Sprintf(i18n.T(ctx, i18n.KeyMailOrderPartRefundedCredit), twd(l.RefundCents), twd(l.CreditCents))
	}
	body := fmt.Sprintf(i18n.T(ctx, i18n.KeyMailOrderPartRefundedBody), l.OrderNumber,
		strings.Join(items, i18n.T(ctx, i18n.KeyListSeparator)), refunded, n.orderURL(l.OrderNumber))
	return n.send(ctx, &Message{
		To:      l.Address,
		Subject: fmt.Sprintf(i18n.T(ctx, i18n.KeyMailOrderPartRefundedSubject), l.OrderNumber),
		Body:    n.letter(ctx, l.Name, body),
	})
}
