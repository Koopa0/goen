package feedback

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) Messages(ctx context.Context, after ...string) (admin.MessagesView, error) {
	const scope = "/admin/messages"
	from, resumed := web.ResumeKeyset(scope, after, func(p messagePosition) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminMessages(ctx, db.AdminMessagesParams{HasCursor: resumed, AfterRank: from.Rank, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.MessagesView{}, fmt.Errorf("read contact messages: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminMessagesRow) string { return r.PageCursor })
	view := admin.MessagesView{
		Bound: bound,
		Rows:  make([]admin.Message, 0, len(rows)),
	}
	for i := range rows {
		m := &rows[i]
		view.Rows = append(view.Rows, admin.Message{
			ID: m.ID.String(), Name: m.Name, Email: m.Email, Subject: m.Subject,
			OrderRef: m.OrderRef, Message: m.Message,
			Handled: m.HandledAt.Valid,
			At:      shoptime.Minute(m.CreatedAt),
			// Computed in the query: subtracting Go's now() from a database-written
			// created_at would compare two clocks.
			WaitingDays: int(m.WaitingDays),
		})
	}
	return view, nil
}

func (s *Store) SetMessageHandled(ctx context.Context, id string, handled bool) error {
	messageID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	action := audit.ActionReopenMessage
	if handled {
		action = audit.ActionHandleMessage
	}

	return audit.Run(ctx, s.pool, audit.Event{
		Action: action, Table: "contact_messages", ID: audit.EntityID(messageID),
		Before: nil,
		After:  map[string]any{"message_id": id, "handled": handled},
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
				return ErrNotFound
			}
			return nil
		})
}
