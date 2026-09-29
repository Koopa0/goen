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
	TerminalCancelledByCustomer        TerminalKind = "cancelled_by_customer"
	TerminalCancelledByStaff           TerminalKind = "cancelled_by_staff"
	TerminalCancelledByPaymentDeadline TerminalKind = "cancelled_by_payment_deadline"
	TerminalDelivered                  TerminalKind = "delivered"
	TerminalCollected                  TerminalKind = "collected"
)

// OrderTerminal is what an order.terminal message carries: the order and the
// fact, never an address.
type OrderTerminal struct {
	OrderID uuid.UUID    `json:"order_id"`
	Kind    TerminalKind `json:"kind"`
	// Refunded says money may have reached the provider for this unpaid order
	// and has been or will be returned. Absent from an older producer, it reads
	// false.
	Refunded bool `json:"refunded"`
}

// TerminalRecipient is resolved at delivery, never retained in the outbox.
type TerminalRecipient struct {
	Address, Name, Locale, OrderNumber string
}

// SendOrderTerminal reports an order fact without promising a new delivery. A
// cancellation names a refund only when money may have reached the provider.
func (n Notifier) SendOrderTerminal(ctx context.Context, m *OrderTerminal, to TerminalRecipient) error {
	if !Valid(to.Address) {
		return errors.New("terminal order notice has no usable recipient")
	}
	ctx = n.locale(ctx, to.Locale)
	subject, body := i18n.KeyMailOrderArrivedSubject, i18n.KeyMailOrderDeliveredBody
	switch m.Kind {
	case TerminalCancelledByCustomer:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderCustomerCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderCustomerCancelledRefundBody
		}
	case TerminalCancelledByStaff:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderStaffCancelledBody
	case TerminalCancelledByPaymentDeadline:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderDeadlineCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderDeadlineCancelledRefundBody
		}
	case TerminalCollected:
		body = i18n.KeyMailOrderCollectedBody
	case TerminalDelivered:
	default:
		return fmt.Errorf("unknown terminal order notice %q", m.Kind)
	}
	return n.sender.Send(ctx, &Message{To: to.Address, Subject: fmt.Sprintf(i18n.T(ctx, subject), to.OrderNumber), Body: n.letter(ctx, to.Name, fmt.Sprintf(i18n.T(ctx, body), to.OrderNumber, n.orderURL(to.OrderNumber)))})
}
