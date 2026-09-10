package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	email2 "github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"

	"github.com/koopa0/goen/internal/db"
)

// ResetTokenTTL is how long a reset link works.
const ResetTokenTTL = time.Hour

// ErrResetInvalid is a token that is unknown, spent or expired: one error for all three.
var ErrResetInvalid = errors.New("account: that reset link is not usable")

// ErrInvalidPassword is a new password the rules refuse.
var ErrInvalidPassword = errors.New("account: password refused")

// beginReset atomically issues a reset token and queues its message when the
// address belongs to an account. Earlier unused tokens die in the same
// transaction, so a replacement link is the only one that can still spend. An
// unknown address is the same nil result: the caller must not become an
// account-existence oracle.
func (s *Store) beginReset(ctx context.Context, email string) error {
	email = email2.Clean(email)
	if EmailError(email) != "" {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.UserForPasswordReset(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lock reset account: %w", err)
	}

	token, err := NewToken()
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(token))
	payload, err := json.Marshal(email2.PasswordReset{
		Email: row.Email, Token: token, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("encode reset message: %w", err)
	}

	if err := q.InvalidateResetTokens(ctx, row.ID); err != nil {
		return fmt.Errorf("invalidate prior reset tokens: %w", err)
	}
	if err := q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: digest[:],
		UserID:    row.ID,
		Ttl:       pgtype.Interval{Microseconds: ResetTokenTTL.Microseconds(), Valid: true},
	}); err != nil {
		return fmt.Errorf("create reset token: %w", err)
	}
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     outbox.TopicPasswordReset,
		DedupeKey: "reset:" + hex.EncodeToString(digest[:]),
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("enqueue reset message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

// CompleteReset spends a token and sets the password, in one transaction.
func (s *Store) CompleteReset(ctx context.Context, token, password string) error {
	if why := PasswordError(password); why != "" {
		return fmt.Errorf("%w: %s", ErrInvalidPassword, why)
	}
	digest := sha256.Sum256([]byte(token))

	// A cheap gate before argon2, which costs 64 MiB a call; the UPDATE decides.
	userID, err := s.q.PasswordResetToken(ctx, digest[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResetInvalid
		}
		return fmt.Errorf("read reset token: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reset: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if _, lockErr := q.LockUserForPasswordReset(ctx, userID); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrResetInvalid
		}
		return fmt.Errorf("lock reset account: %w", lockErr)
	}
	spentUserID, err := q.SpendPasswordResetToken(ctx, digest[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResetInvalid
		}
		return fmt.Errorf("spend reset token: %w", err)
	}
	if spentUserID != userID {
		return errors.New("account: reset token changed owner")
	}

	if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ID: userID, PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := q.InvalidateResetTokens(ctx, userID); err != nil {
		return fmt.Errorf("invalidate other reset tokens: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}
