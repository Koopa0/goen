package email

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
)

// TerminalKind is the order fact an order.terminal message reports. It is a
// separate copy of the producer's kinds, so a consumer may lag a version.
type TerminalKind string

// The terminal facts a message may carry.
const (
	TerminalCancelledByCustomer TerminalKind = "cancelled_by_customer"
	TerminalCancelledByStaff    TerminalKind = "cancelled_by_staff"
	TerminalDelivered           TerminalKind = "delivered"
	TerminalCollected           TerminalKind = "collected"
)

// OrderTerminal is what an order.terminal message carries: the order and the
// fact, never an address.
type OrderTerminal struct {
	OrderID uuid.UUID    `json:"order_id"`
	Kind    TerminalKind `json:"kind"`
}

// TerminalRecipient is resolved at delivery, never retained in the outbox.
type TerminalRecipient struct {
	Address, Name, Locale, OrderNumber string
}

// SendOrderTerminal reports an order fact without promising a refund or a new delivery.
func (n Notifier) SendOrderTerminal(ctx context.Context, kind TerminalKind, to TerminalRecipient) error {
	if !Valid(to.Address) {
		return errors.New("terminal order notice has no usable recipient")
	}
	ctx = n.locale(ctx, to.Locale)
	subject, body := i18n.KeyMailOrderArrivedSubject, i18n.KeyMailOrderDeliveredBody
	switch kind {
	case TerminalCancelledByCustomer:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderCustomerCancelledBody
	case TerminalCancelledByStaff:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderStaffCancelledBody
	case TerminalCollected:
		body = i18n.KeyMailOrderCollectedBody
	case TerminalDelivered:
	default:
		return fmt.Errorf("unknown terminal order notice %q", kind)
	}
	return n.sender.Send(ctx, &Message{To: to.Address, Subject: fmt.Sprintf(i18n.T(ctx, subject), to.OrderNumber), Body: n.letter(ctx, to.Name, fmt.Sprintf(i18n.T(ctx, body), to.OrderNumber, n.orderURL(to.OrderNumber)))})
}
