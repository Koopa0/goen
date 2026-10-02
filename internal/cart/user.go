package cart

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ForUser exists because merge-adopt deletes the guest row the cookie still
// names, so the HTTP path must find the surviving cart by user_id.
func (s *Store) ForUser(ctx context.Context, userID string) (uuid.UUID, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return uuid.Nil, ErrNotFound
	}
	row, err := s.q.CartForUser(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("read account cart: %w", err)
	}
	return row, nil
}
