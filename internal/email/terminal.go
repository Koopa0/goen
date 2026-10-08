package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

type TerminalKind string

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
	// Refunded: money taken from the customer has been or will be returned, so
	// the mail must not say nothing was charged. Set from what the cancelling
	// transaction reads, including a payment that may still land. Absent from an
	// older row, it reads false.
	Refunded bool `json:"refunded"`
}

// TerminalRecipient is resolved at delivery, never retained in the outbox.
type TerminalRecipient struct {
	Address, Name, Locale, OrderNumber string
	// RescissionEnds is the last day to return the goods; the zero time before
	// any delivery.
	RescissionEnds time.Time
}

// SendOrderTerminal reports an order fact without promising a new delivery. A
// cancellation names a refund only when money may have reached the provider.
func (n Notifier) SendOrderTerminal(ctx context.Context, m *OrderTerminal, to TerminalRecipient) error {
	if !Valid(to.Address) {
		return errors.New("terminal order notice has no usable recipient")
	}
	ctx = n.locale(ctx, to.Locale)
	subject, body := i18n.KeyMailOrderDeliveredSubject, i18n.KeyMailOrderDeliveredBody
	switch m.Kind {
	case TerminalCancelledByCustomer:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderCustomerCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderCustomerCancelledRefundBody
		}
	case TerminalCancelledByStaff:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderStaffCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderStaffCancelledRefundBody
		}
	case TerminalCancelledByPaymentDeadline:
		subject, body = i18n.KeyMailOrderCancelledSubject, i18n.KeyMailOrderDeadlineCancelledBody
		if m.Refunded {
			body = i18n.KeyMailOrderDeadlineCancelledRefundBody
		}
	case TerminalCollected:
		subject, body = i18n.KeyMailOrderCollectedSubject, i18n.KeyMailOrderCollectedBody
	case TerminalDelivered:
	default:
		return fmt.Errorf("unknown terminal order notice %q", m.Kind)
	}
	text := fmt.Sprintf(i18n.T(ctx, body), to.OrderNumber, n.orderURL(to.OrderNumber))
	if !to.RescissionEnds.IsZero() && (m.Kind == TerminalDelivered || m.Kind == TerminalCollected) {
		text += "\n\n" + fmt.Sprintf(i18n.T(ctx, i18n.KeyMailRescissionEnds), shoptime.DateText(ctx, shoptime.DateOf(to.RescissionEnds, time.Now())))
	}
	return n.send(ctx, &Message{To: to.Address, Subject: fmt.Sprintf(i18n.T(ctx, subject), to.OrderNumber), Body: n.letter(ctx, to.Name, text)})
}
