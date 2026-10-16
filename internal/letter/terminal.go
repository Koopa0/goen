package letter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/shoptime"
)

// SendOrderTerminal reports an order fact without promising a new delivery. A
// cancellation names a refund only when money may have reached the provider.
func (n Notifier) SendOrderTerminal(ctx context.Context, m *outbox.OrderTerminal, to notice.Recipient) error {
	if !email.Valid(to.Address) {
		return errors.New("terminal order notice has no usable recipient")
	}
	ctx = n.locale(ctx, to.Locale)
	subject, body := i18n.KeyMailOrderDeliveredSubject, i18n.KeyMailOrderDeliveredBody
	switch m.Kind {
	case outbox.TerminalCancelledByCustomer:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderCustomerCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderCustomerCancelledRefundBody
		}
	case outbox.TerminalCancelledByStaff:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderStaffCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderStaffCancelledRefundBody
		}
	case outbox.TerminalCancelledByPaymentDeadline:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderDeadlineCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderDeadlineCancelledRefundBody
		}
	case outbox.TerminalCollected:
		subject, body = i18n.KeyMailOrderCollectedSubject, i18n.KeyMailOrderCollectedBody
	case outbox.TerminalDelivered:
	default:
		return fmt.Errorf("unknown terminal order notice %q", m.Kind)
	}
	text := fmt.Sprintf(i18n.T(ctx, body), to.OrderNumber, n.orderURL(to.OrderNumber))
	if !to.RescissionEnds.IsZero() && (m.Kind == outbox.TerminalDelivered || m.Kind == outbox.TerminalCollected) {
		text += "\n\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRescissionEnds), shoptime.DateText(ctx, shoptime.DateOf(to.RescissionEnds, time.Now())))
	}
	return n.send(ctx, &Message{To: to.Address, Subject: fmt.Sprintf(i18n.T(ctx, subject), to.OrderNumber), Body: n.letter(ctx, to.Name, text)})
}
