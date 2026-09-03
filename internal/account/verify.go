package account

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

// VerifyTokenTTL is how long a verification link works.
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

// requestVerification atomically asks for addr to be proved and queues the
// link. The token is operation-local; the account keeps its old address until
// the queued link is followed.
func (s *Store) requestVerification(ctx context.Context, userID, addr string) error {
	id, parseErr := uuid.Parse(userID)
	if parseErr != nil {
		return fmt.Errorf("parse user id: %w", parseErr)
	}
	addr = email.Clean(addr)
	if EmailError(addr) != "" {
		return fmt.Errorf("requesting verification of %q: not a usable address", addr)
	}

	taken, takenErr := s.q.EmailBelongsToSomebodyElse(ctx, db.EmailBelongsToSomebodyElseParams{
		Email: addr, UserID: id,
	})
	if takenErr != nil {
		return fmt.Errorf("check whether %q is taken: %w", addr, takenErr)
	}
	if taken {
		return ErrEmailTaken
	}

	token, tokenErr := NewToken()
	if tokenErr != nil {
		return tokenErr
	}

	tx, beginErr := s.pool.Begin(ctx)
	if beginErr != nil {
		return fmt.Errorf("begin verification request: %w", beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	if _, lockErr := q.LockUserForEmailVerification(ctx, id); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock verification account: %w", lockErr)
	}
	if deleteErr := q.DeleteOutstandingEmailVerificationMessage(ctx, id); deleteErr != nil {
		return fmt.Errorf("remove superseded verification message: %w", deleteErr)
	}

	if reqErr := q.RequestEmailVerification(ctx, db.RequestEmailVerificationParams{
		UserID: id, Email: addr, Digest: HashToken(token),
		Ttl: pgtype.Interval{Microseconds: int64(VerifyTokenTTL / time.Microsecond), Valid: true},
	}); reqErr != nil {
		return fmt.Errorf("record verification request: %w", reqErr)
	}

	payload, marshalErr := json.Marshal(email.AddressVerify{
		Email: addr, Token: token, Locale: i18n.FromContext(ctx).Tag(),
	})
	if marshalErr != nil {
		return fmt.Errorf("encode verification message: %w", marshalErr)
	}
	digest := HashToken(token)
	if err := q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic:     outbox.TopicEmailVerify,
		DedupeKey: "verify:" + hex.EncodeToString(digest),
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("enqueue verification message: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verification request: %w", err)
	}
	return nil
}

// ConfirmVerification spends a link, moves the address and marks it proved.
func (s *Store) ConfirmVerification(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrVerifyInvalid
	}
	digest := HashToken(token)
	verification, err := usableVerification(ctx, s.q, digest)
	if err != nil {
		return "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin verification: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	lockedUser, err := lockVerificationAccount(ctx, q, verification.UserID)
	if err != nil {
		return "", err
	}
	row, err := spendMatchingVerification(ctx, q, digest, verification)
	if err != nil {
		return "", err
	}

	if err := setVerifiedEmail(ctx, q, row); err != nil {
		return "", err
	}
	// A reset link was sent to the old address. Once that address no longer
	// identifies this account, its holder must not be able to choose a password.
	if err := retireOldMailboxResets(ctx, q, lockedUser.Email, row); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit verification: %w", err)
	}
	return row.Email, nil
}

func usableVerification(
	ctx context.Context,
	q *db.Queries,
	digest []byte,
) (db.EmailVerificationTokenRow, error) {
	verification, err := q.EmailVerificationToken(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.EmailVerificationTokenRow{}, ErrVerifyInvalid
	}
	if err != nil {
		return db.EmailVerificationTokenRow{}, fmt.Errorf("read verification: %w", err)
	}
	return verification, nil
}

func lockVerificationAccount(
	ctx context.Context,
	q *db.Queries,
	userID uuid.UUID,
) (db.LockUserForEmailVerificationRow, error) {
	user, err := q.LockUserForEmailVerification(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.LockUserForEmailVerificationRow{}, ErrVerifyInvalid
	}
	if err != nil {
		return db.LockUserForEmailVerificationRow{}, fmt.Errorf("lock verification account: %w", err)
	}
	return user, nil
}

func spendMatchingVerification(
	ctx context.Context,
	q *db.Queries,
	digest []byte,
	expected db.EmailVerificationTokenRow,
) (db.SpendEmailVerificationRow, error) {
	spent, err := q.SpendEmailVerification(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SpendEmailVerificationRow{}, ErrVerifyInvalid
	}
	if err != nil {
		return db.SpendEmailVerificationRow{}, fmt.Errorf("spend verification: %w", err)
	}
	if spent.UserID != expected.UserID || spent.Email != expected.Email {
		return db.SpendEmailVerificationRow{}, errors.New("account: verification changed owner or address")
	}
	return spent, nil
}

func setVerifiedEmail(ctx context.Context, q *db.Queries, verified db.SpendEmailVerificationRow) error {
	err := q.SetVerifiedEmail(ctx, db.SetVerifiedEmailParams{
		UserID: verified.UserID, Email: verified.Email,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		// Taken between the request and now; the transaction leaves the link unspent.
		return ErrEmailTaken
	}
	if err != nil {
		return fmt.Errorf("set verified email: %w", err)
	}
	return nil
}

func retireOldMailboxResets(
	ctx context.Context,
	q *db.Queries,
	oldEmail string,
	verified db.SpendEmailVerificationRow,
) error {
	if strings.EqualFold(oldEmail, verified.Email) {
		return nil
	}
	if err := q.InvalidateResetTokens(ctx, verified.UserID); err != nil {
		return fmt.Errorf("invalidate reset tokens after email change: %w", err)
	}
	if err := q.DeletePasswordResetMessagesForEmail(ctx, oldEmail); err != nil {
		return fmt.Errorf("purge old-mailbox reset messages: %w", err)
	}
	return nil
}
