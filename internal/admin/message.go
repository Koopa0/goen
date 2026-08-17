package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Messages reads the customer-service inbox, handled ones included.
func (s *Store) Messages(ctx context.Context) (pages.AdminMessagesView, error) {
	rows, err := s.q.AdminMessages(ctx, PageSize)
	if err != nil {
		return pages.AdminMessagesView{}, fmt.Errorf("read contact messages: %w", err)
	}
	view := pages.AdminMessagesView{Rows: make([]pages.AdminMessage, 0, len(rows))}
	for i := range rows {
		m := &rows[i]
		view.Rows = append(view.Rows, pages.AdminMessage{
			ID: m.ID.String(), Name: m.Name, Email: m.Email, Subject: m.Subject,
			OrderRef: m.OrderRef, Message: m.Message,
			Handled: m.HandledAt.Valid,
			At:      m.CreatedAt.Format("2006-01-02 15:04"),
			// From the query, by the DATABASE's clock: created_at is written
			// there, and subtracting Go's time.Now() compares two clocks.
			WaitingDays: int(m.WaitingDays),
		})
	}
	return view, nil
}

// SetMessageHandled marks a message dealt with, or puts it back in the queue.
// Two calls rather than a toggle, so a double-submitted form cannot reopen what
// was just handled.
func (s *Store) SetMessageHandled(ctx context.Context, id string, handled bool) error {
	messageID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	action := ActionReopenMessage
	if handled {
		action = ActionHandleMessage
	}

	return s.audited(ctx, Event{
		Action: action, Table: "contact_messages", ID: nullableID(messageID),
		Before: nil,
		// Never the message itself: audit_events is append-only where
		// contact_messages is not.
		After: map[string]any{"message_id": id, "handled": handled},
	},
		func(ctx context.Context, q *db.Queries) error {
			var n int64
			var setErr error
			if handled {
				n, setErr = q.HandleMessage(ctx, messageID)
			} else {
				n, setErr = q.ReopenMessage(ctx, messageID)
			}
			if setErr != nil {
				return fmt.Errorf("set message handled: %w", setErr)
			}
			if n == 0 {
				// Already in the state asked for, or gone.
				return ErrNotFound
			}
			return nil
		})
}
