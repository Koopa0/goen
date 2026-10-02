package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MessagesView is the customer-service inbox.
type MessagesView struct {
	pages.ListBound

	Rows   []Message
	Notice string
}

// Message is one thing a customer wrote in about.
type Message struct {
	ID          string
	Name        string
	Email       string
	Subject     string
	OrderRef    string
	Message     string
	Handled     bool
	At          string
	WaitingDays int
}

// Empty reports whether nobody has written in.
func (v MessagesView) Empty() bool { return len(v.Rows) == 0 }

// OpenCount is how many are still waiting.
func (v MessagesView) OpenCount() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Handled {
			n++
		}
	}
	return n
}

// OpenCountText is that number for the page.
func (v MessagesView) OpenCountText() string { return strconv.Itoa(v.OpenCount()) }

// HasOrderRef reports whether the customer quoted an order.
func (m Message) HasOrderRef() bool { return m.OrderRef != "" }

// OrderHref is the order they quoted, which may name nothing.
func (m Message) OrderHref() string { return "/admin/orders/" + m.OrderRef }

// Waiting is how long it has been unanswered, in words.
func (m Message) Waiting(ctx context.Context) string {
	switch {
	case m.Handled:
		return i18n.T(ctx, i18n.KeyAdminMsgHandled)
	case m.WaitingDays == 0:
		return i18n.T(ctx, i18n.KeyAdminMsgToday)
	case m.WaitingDays == 1:
		return i18n.T(ctx, i18n.KeyAdminMsgOneDay)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminMsgDays), m.WaitingDays)
	}
}

// Overdue reports whether it has waited three days or more.
func (m Message) Overdue() bool { return !m.Handled && m.WaitingDays >= 3 }

// Action is where the toggle posts; separate paths, so a double submit
func (m Message) Action() string {
	if m.Handled {
		return "/admin/messages/reopen"
	}
	return "/admin/messages/handle"
}

// ActionLabel is what the button says.
func (m Message) ActionLabel(ctx context.Context) string {
	if m.Handled {
		return i18n.T(ctx, i18n.KeyAdminMsgReopen)
	}
	return i18n.T(ctx, i18n.KeyAdminMsgHandle)
}
