package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminMessagesView is the customer-service inbox.
//
// The dashboard has counted unhandled messages since the back office shipped and
// there was no page to open them: a customer wrote in, and the shop saw a number.
type AdminMessagesView struct {
	Rows   []AdminMessage
	Notice string
}

// AdminMessage is one thing a customer wrote in about.
type AdminMessage struct {
	ID       string
	Name     string
	Email    string
	Subject  string
	OrderRef string
	Message  string
	Handled  bool
	At       string
	// WaitingDays is how long an unhandled message has been waiting. It is the
	// number that decides what to do next, and a date makes a reader work it out.
	WaitingDays int
}

// Empty reports whether nobody has written in.
func (v AdminMessagesView) Empty() bool { return len(v.Rows) == 0 }

// OpenCount is how many are still waiting.
func (v AdminMessagesView) OpenCount() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Handled {
			n++
		}
	}
	return n
}

// OpenCountText is that number for the page.
func (v AdminMessagesView) OpenCountText() string { return strconv.Itoa(v.OpenCount()) }

// HasOrderRef reports whether the customer quoted an order.
func (m AdminMessage) HasOrderRef() bool { return m.OrderRef != "" }

// OrderHref is the back-office page for the order they quoted.
//
// A LINK, because "my order hasn't arrived" is answered by looking at the order
// and a staff member should not have to retype the number to get there. It is
// what the CUSTOMER typed, so it may name nothing — the admin order page answers
// a 404 for that, which is the honest outcome.
func (m AdminMessage) OrderHref() string { return "/admin/orders/" + m.OrderRef }

// Waiting is how long it has been unanswered, in words.
func (m AdminMessage) Waiting(ctx context.Context) string {
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

// Overdue reports whether it has waited long enough to be embarrassing.
//
// Three days. Not a threshold anybody asked for, but a queue with no notion of
// late is a queue where the oldest item is just the last row.
func (m AdminMessage) Overdue() bool { return !m.Handled && m.WaitingDays >= 3 }

// Action is where the toggle posts. Separate paths rather than one toggle, so a
// double-submitted form cannot reopen what was just handled.
func (m AdminMessage) Action() string {
	if m.Handled {
		return "/admin/messages/reopen"
	}
	return "/admin/messages/handle"
}

// ActionLabel is what the button says.
func (m AdminMessage) ActionLabel(ctx context.Context) string {
	if m.Handled {
		return i18n.T(ctx, i18n.KeyAdminMsgReopen)
	}
	return i18n.T(ctx, i18n.KeyAdminMsgHandle)
}
