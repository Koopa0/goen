// Package messages is the staff queue of contact messages customers sent; it
// holds the list, handle and reopen; it does not hold outgoing mail (outbox,
// letter) or catalogue messages (i18n).
package messages

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/shoptime"
)

func (s *Store) Messages(ctx context.Context, after string) (View, error) {
	const scope = "/admin/messages"
	from, resumed := keyset.Resume(scope, after, func(p messagePosition) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminMessages(ctx, db.AdminMessagesParams{HasCursor: resumed, AfterRank: from.Rank, AfterAt: from.At, AfterID: from.ID, RowLimit: keyset.PageLimit})
	if err != nil {
		return View{}, fmt.Errorf("read contact messages: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminMessagesRow) string { return r.PageCursor })
	view := View{
		Bound: bound,
		Rows:  make([]Message, 0, len(rows)),
	}
	for i := range rows {
		m := &rows[i]
		label, labelErr := subjectLabel(ctx, contact.Subject(m.Subject))
		if labelErr != nil {
			return View{}, fmt.Errorf("read contact messages: %w", labelErr)
		}
		view.Rows = append(view.Rows, Message{
			ID: m.ID.String(), Name: m.Name, Email: m.Email, SubjectLabel: label,
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

func subjectLabel(ctx context.Context, subject contact.Subject) (string, error) {
	key, ok := subject.LabelKey()
	if !ok {
		return "", fmt.Errorf("unknown contact subject %q", subject)
	}
	return i18n.T(ctx, key), nil
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
		After:  audit.State{audit.FieldMessageID: id, audit.FieldHandled: handled},
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
