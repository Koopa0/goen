package email

import (
	"context"
	"errors"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ordernotice"
)

// TerminalRecipient is resolved at delivery, never retained in the outbox.
type TerminalRecipient struct {
	Address, Name, Locale, OrderNumber string
}

// SendOrderTerminal reports an order fact without promising a refund or a new delivery.
func (n Notifier) SendOrderTerminal(ctx context.Context, kind ordernotice.Kind, to TerminalRecipient) error {
	if !Valid(to.Address) {
		return errors.New("terminal order notice has no usable recipient")
	}
	ctx = n.locale(ctx, to.Locale)
	subject, body := i18n.KeyMailOrderArrivedSubject, i18n.KeyMailOrderDeliveredBody
	switch kind {
	case ordernotice.CancelledByCustomer:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderCustomerCancelledBody
	case ordernotice.CancelledByStaff:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderStaffCancelledBody
	case ordernotice.Collected:
		body = i18n.KeyMailOrderCollectedBody
	case ordernotice.Delivered:
	default:
		return fmt.Errorf("unknown terminal order notice %q", kind)
	}
	return n.sender.Send(ctx, &Message{To: to.Address, Subject: fmt.Sprintf(i18n.T(ctx, subject), to.OrderNumber), Body: n.letter(ctx, to.Name, fmt.Sprintf(i18n.T(ctx, body), to.OrderNumber, n.orderURL(to.OrderNumber)))})
}
