package account

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	email2 "github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// VerifyTokenTTL is how long a verification link works. Two days, because this
// proves an address rather than being a credential the way a reset link is.
const VerifyTokenTTL = 48 * time.Hour

// ErrVerifyInvalid is a link that is unknown, spent or expired.
var ErrVerifyInvalid = errors.New("account: that verification link is not usable")

// Verification is what the account page shows about the customer's address.
type Verification struct {
	Verified     bool
	PendingEmail string
}

// EmailVerification reads what the account page needs.
func (s *Store) EmailVerification(ctx context.Context, userID string) (Verification, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Verification{}, fmt.Errorf("parse user id: %w", err)
	}
	row, err := s.q.EmailVerification(ctx, id)
	if err != nil {
		return Verification{}, fmt.Errorf("read verification state: %w", err)
	}
	return Verification{Verified: row.Verified, PendingEmail: row.PendingEmail}, nil
}

// RequestVerification asks for addr to be proved and returns the token for the
// link. The account keeps its OLD address until the link is followed, so a
// mistyped change leaves receipts and reset links still arriving.
func (s *Store) RequestVerification(ctx context.Context, userID, addr string) (string, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	addr = email2.Clean(addr)
	if EmailError(addr) != "" {
		return "", fmt.Errorf("requesting verification of %q: not a usable address", addr)
	}

	taken, takenErr := s.q.EmailBelongsToSomebodyElse(ctx, db.EmailBelongsToSomebodyElseParams{
		Email: addr, UserID: id,
	})
	if takenErr != nil {
		return "", fmt.Errorf("check whether %q is taken: %w", addr, takenErr)
	}
	if taken {
		return "", ErrEmailTaken
	}

	token, err := NewToken()
	if err != nil {
		return "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin verification request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if reqErr := q.RequestEmailVerification(ctx, db.RequestEmailVerificationParams{
		UserID: id, Email: addr, Digest: HashToken(token),
		Ttl: pgtype.Interval{Microseconds: int64(VerifyTokenTTL / time.Microsecond), Valid: true},
	}); reqErr != nil {
		return "", fmt.Errorf("record verification request: %w", reqErr)
	}

	payload, err := json.Marshal(email2.EmailVerify{
		Email: addr, Token: token, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return "", fmt.Errorf("encode verification message: %w", err)
	}
	digest := HashToken(token)
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     outbox.TopicEmailVerify,
		DedupeKey: "verify:" + hex.EncodeToString(digest),
		Payload:   payload,
	}); err != nil {
		return "", fmt.Errorf("enqueue verification message: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit verification request: %w", err)
	}
	return token, nil
}

// ConfirmVerification spends a link, moves the address and marks it proved.
func (s *Store) ConfirmVerification(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrVerifyInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.SpendEmailVerification(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrVerifyInvalid
		}
		return "", fmt.Errorf("spend verification: %w", err)
	}

	if err := q.SetVerifiedEmail(ctx, db.SetVerifiedEmailParams{
		UserID: row.UserID, Email: row.Email,
	}); err != nil {
		// Taken between the request and now. The whole transaction rolls back, so
		// the link is unspent and the customer can ask again for a different one.
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			return "", ErrEmailTaken
		}
		return "", fmt.Errorf("set verified email: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit verification: %w", err)
	}
	return row.Email, nil
}
