package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func enqueueStaffInvitation(ctx context.Context, q *db.Queries, userID uuid.UUID) error {
	// A retained delivery belongs to one grant; a later re-grant needs its own notice.
	return outbox.Enqueue(ctx, q, outbox.TopicStaffInvitation, "staff-invite:"+uuid.NewString(), &email.StaffInvitation{
		// The admin's locale, because users stores none for the invitee.
		UserID: userID.String(), Locale: i18n.FromContext(ctx).Tag(),
	})
}

// InvitationRecipient resolves only an account that still has staff access.
// Keeping its address out of the queued payload prevents delivery from reviving
// the address of an erased account or inviting someone whose grant was revoked.
func (s *Store) InvitationRecipient(ctx context.Context, userID string) (address, name string, err error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", "", fmt.Errorf("parse invited user: %w", err)
	}
	row, err := s.q.StaffInvitationRecipient(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("read invited user: %w", err)
	}
	return row.Email, row.FullName, nil
}
