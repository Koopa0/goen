package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/ui/pages"
)

func (s *Store) creditRecipient(ctx context.Context, view *pages.AdminCreditView) error {
	user, err := s.q.CustomerByEmail(ctx, strings.TrimSpace(view.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read credit recipient: %w", err)
	}
	balance, err := s.q.CreditBalance(ctx, uuid.NullUUID{UUID: user.ID, Valid: true})
	if err != nil {
		return fmt.Errorf("read recipient balance: %w", err)
	}
	view.CustomerID, view.CustomerName, view.Email, view.BalanceCents = user.ID.String(), user.FullName, user.Email, balance
	return nil
}
