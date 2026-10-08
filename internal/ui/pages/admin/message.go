package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

type MessagesView struct {
	web.Bound

	Rows   []Message
	Notice components.Result
}

type Message struct {
	ID           string
	Name         string
	Email        string
	SubjectLabel string
	OrderRef     string
	Message      string
	Handled      bool
	At           string
	WaitingDays  int
}

func (v MessagesView) Empty() bool { return len(v.Rows) == 0 }

func (v MessagesView) OpenCount() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Handled {
			n++
		}
	}
	return n
}

func (v MessagesView) OpenCountText() string { return strconv.Itoa(v.OpenCount()) }

func (m Message) HasOrderRef() bool { return m.OrderRef != "" }

func (m Message) OrderHref() string { return "/admin/orders/" + m.OrderRef }

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

// OverdueText says "overdue" in words, so the badge does not rest on its colour alone.
func (m Message) OverdueText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminMsgOverdue), m.Waiting(ctx))
}

func (m Message) Overdue() bool { return !m.Handled && m.WaitingDays >= 3 }

func (m Message) Action() string {
	if m.Handled {
		return "/admin/messages/reopen"
	}
	return "/admin/messages/handle"
}

func (m Message) ActionLabel(ctx context.Context) string {
	if m.Handled {
		return i18n.T(ctx, i18n.KeyAdminMsgReopen)
	}
	return i18n.T(ctx, i18n.KeyAdminMsgHandle)
}
