package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	mailmsg "github.com/koopa0/goen/internal/email"
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

// requestReset queues a forgotten-password request and nothing else. Every
// address the rules accept is the same single statement, so the caller cannot
// become an account-existence oracle through what it answers or how long it
// takes; [Store.IssueReset] does the work that only a real account needs.
func (s *Store) requestReset(ctx context.Context, addr string) error {
	return s.queueResetRequest(ctx, addr, "reset-request:"+uuid.NewString())
}

func (s *Store) queueResetRequest(ctx context.Context, addr, dedupeKey string) error {
	addr = mailmsg.Clean(addr)
	if EmailError(addr) != "" {
		return nil
	}
	if err := s.q.EnqueuePasswordResetRequest(ctx, db.EnqueuePasswordResetRequestParams{
		Topic:     outbox.TopicPasswordResetRequest.Name(),
		DedupeKey: dedupeKey,
		Locale:    i18n.FromContext(ctx).Tag(),
		Email:     addr,
	}); err != nil {
		return fmt.Errorf("queue password reset request: %w", err)
	}
	return nil
}

// IssueReset is the outbox's half of a forgotten-password request: it
// atomically issues a reset token and queues its message when the request named
// an account that still exists. Earlier unused tokens die in the same
// transaction, so a replacement link is the only one that can still spend. The
// address is read here rather than carried in the request, so the link goes to
// the account's current address.
func (s *Store) IssueReset(ctx context.Context, req *outbox.PasswordResetRequest) error {
	if req.UserID == "" {
		return nil
	}
	id, err := uuid.Parse(req.UserID)
	if err != nil {
		return fmt.Errorf("parse reset request account: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.UserForPasswordReset(ctx, id)
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
	if err := outbox.Enqueue(ctx, q, outbox.TopicPasswordReset, "reset:"+hex.EncodeToString(digest[:]),
		&mailmsg.PasswordReset{Email: row.Email, Token: token, Locale: req.Locale}); err != nil {
		return err
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

	if err := applyReset(ctx, q, userID, hash); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}

// applyReset is what a spent reset link does to its account, inside the
// transaction that spent it.
func applyReset(ctx context.Context, q *db.Queries, userID uuid.UUID, hash string) error {
	if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{
		ID: userID, PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := q.ProveEmailByReset(ctx, userID); err != nil {
		return fmt.Errorf("prove the address the link went to: %w", err)
	}
	if err := q.InvalidateResetTokens(ctx, userID); err != nil {
		return fmt.Errorf("invalidate other reset tokens: %w", err)
	}
	if err := q.DeleteUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("end sessions: %w", err)
	}
	return nil
}
