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

// ErrResetInvalid is a token that is unknown, spent, or expired — one error for
// all three, so probing tokens says nothing about which guess was real.
var ErrResetInvalid = errors.New("account: that reset link is not usable")

// ErrInvalidPassword is a new password the rules refuse.
var ErrInvalidPassword = errors.New("account: password refused")

// BeginReset issues a reset token for an email address, if it belongs to
// anybody. The caller must answer identically whether or not found is true, or
// the form is an oracle for which addresses are registered.
func (s *Store) BeginReset(ctx context.Context, email string) (token, sendTo string, found bool, err error) {
	email = email2.Clean(email)
	if EmailError(email) != "" {
		return "", "", false, nil
	}

	row, err := s.q.UserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("read user: %w", err)
	}

	token, err = NewToken()
	if err != nil {
		return "", "", false, err
	}
	digest := sha256.Sum256([]byte(token))
	if err := s.q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
		TokenHash: digest[:],
		UserID:    row.ID,
		Ttl:       pgtype.Interval{Microseconds: ResetTokenTTL.Microseconds(), Valid: true},
	}); err != nil {
		return "", "", false, fmt.Errorf("create reset token: %w", err)
	}
	return token, row.Email, true, nil
}

// CompleteReset spends a token and sets the password, in one transaction.
func (s *Store) CompleteReset(ctx context.Context, token, password string) error {
	if why := PasswordError(password); why != "" {
		return fmt.Errorf("%w: %s", ErrInvalidPassword, why)
	}
	digest := sha256.Sum256([]byte(token))

	// A cheap gate before argon2: without it anybody spends 64 MiB and 28 ms of
	// this process per garbage token they invent. The UPDATE below is the
	// authoritative check.
	if _, err := s.q.PasswordResetToken(ctx, digest[:]); err != nil {
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
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	userID, err := q.SpendPasswordResetToken(ctx, digest[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrResetInvalid
		}
		return fmt.Errorf("spend reset token: %w", err)
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

// EnqueueReset writes the reset message to the outbox.
func (s *Store) EnqueueReset(ctx context.Context, email, token string) error {
	digest := sha256.Sum256([]byte(token))
	payload, err := json.Marshal(email2.PasswordReset{
		Email: email, Token: token, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("encode reset message: %w", err)
	}
	if err := s.q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     outbox.TopicPasswordReset,
		DedupeKey: "reset:" + hex.EncodeToString(digest[:]),
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("enqueue reset message: %w", err)
	}
	return nil
}
