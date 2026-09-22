package twofactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func enqueueStaffInvitation(ctx context.Context, q *db.Queries, userID uuid.UUID) error {
	payload, err := json.Marshal(struct {
		UserID string `json:"user_id"`
		Locale string `json:"locale"`
	}{userID.String(), i18n.FromContext(ctx).Tag()})
	if err != nil {
		return fmt.Errorf("encode staff invitation: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{Topic: outbox.TopicStaffInvitation, DedupeKey: "staff-invite:" + userID.String(), Payload: payload}); err != nil {
		return fmt.Errorf("enqueue staff invitation: %w", err)
	}
	return nil
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
