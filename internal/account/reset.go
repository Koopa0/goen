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
	"github.com/koopa0/goen/internal/pgtx"

	"github.com/koopa0/goen/internal/db"
)

const ResetTokenTTL = time.Hour

// ErrResetInvalid is one error for a token that is unknown, spent or expired.
var ErrResetInvalid = errors.New("account: that reset link is not usable")

var ErrInvalidPassword = errors.New("account: password refused")

// requestReset is the same single statement for every address the rules accept,
// so the caller is no account-existence oracle by what it answers or how long
// it takes; [Store.IssueReset] does the work only a real account needs.
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

// IssueReset kills earlier unused tokens in the same transaction, so a
// replacement link is the only one that can still spend. The address is read
// here rather than carried in the request, so the link goes to the account's
// current address.
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
	defer pgtx.Rollback(ctx, tx)
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

// DeliverPasswordReset checks at delivery because the queue can outlive a
// token's expiry or the request that replaced it. Obsolete mail completes
// without sending; read and delivery failures remain retryable.
func (s *Store) DeliverPasswordReset(ctx context.Context, p *mailmsg.PasswordReset, send func(context.Context, *mailmsg.PasswordReset) error) error {
	if _, err := s.q.PasswordResetToken(ctx, HashToken(p.Token)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("read password reset token for delivery: %w", err)
	}
	return send(ctx, p)
}

func (s *Store) CompleteReset(ctx context.Context, token, password string) (string, error) {
	if why := PasswordError(password); why != "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidPassword, why)
	}
	digest := sha256.Sum256([]byte(token))

	userID, err := s.q.PasswordResetToken(ctx, digest[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrResetInvalid
		}
		return "", fmt.Errorf("read reset token: %w", err)
	}

	hash, err := HashPassword(password)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin reset: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	if _, lockErr := q.LockUserForPasswordReset(ctx, userID); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return "", ErrResetInvalid
		}
		return "", fmt.Errorf("lock reset account: %w", lockErr)
	}
	resetAccount, err := q.UserByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("read reset account: %w", err)
	}
	spentUserID, err := q.SpendPasswordResetToken(ctx, digest[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrResetInvalid
		}
		return "", fmt.Errorf("spend reset token: %w", err)
	}
	if spentUserID != userID {
		return "", errors.New("account: reset token changed owner")
	}

	if err := applyReset(ctx, q, userID, hash); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit reset: %w", err)
	}
	return resetAccount.Email, nil
}

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
