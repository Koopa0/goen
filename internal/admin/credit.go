package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func (s *Store) creditRecipient(ctx context.Context, view *admin.CreditView) error {
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

func validateCreditGrant(view *admin.CreditView) (uuid.UUID, bool) {
	cents, amountOK := positiveDollarsToCents(view.Amount, MaxCreditGrant)
	view.GrantCents = cents
	view.AmountInvalid = !amountOK
	view.ReasonInvalid = strings.TrimSpace(view.Reason) == "" || utf8.RuneCountInString(view.Reason) > MaxCreditReasonRunes
	operationID, err := uuid.Parse(view.OperationID)
	if err != nil || operationID == uuid.Nil {
		view.OperationID = uuid.NewString()
		return uuid.Nil, false
	}
	return operationID, !view.AmountInvalid && !view.ReasonInvalid
}
