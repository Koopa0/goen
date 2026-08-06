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
//
// One hour. Long enough for somebody to find the mail and act on it, short
// enough that a link sitting in an abandoned mailbox is not a standing key to
// the account. The link IS the credential — anybody holding it can set the
// password — so its lifetime is the whole exposure.
const ResetTokenTTL = time.Hour

// ErrResetInvalid is a token that is unknown, spent, or expired.
//
// One error for all three, deliberately: telling them apart tells somebody
// probing tokens which of their guesses was ever real, and the customer's next
// step is the same in every case — ask for a new link.
var ErrResetInvalid = errors.New("account: that reset link is not usable")

// ErrInvalidPassword is a new password the rules refuse. Distinct from
// ErrResetInvalid because the caller's next step differs: this one re-renders
// the form with the reason, rather than sending somebody back to ask for a new
// link.
var ErrInvalidPassword = errors.New("account: password refused")

// BeginReset issues a reset token for an email address, if it belongs to
// anybody.
//
// It returns the token and the address to send it to. found is false when
// nothing matches — and the CALLER must answer the same either way. A form that
// says "no such account" is an oracle for which addresses are registered, and
// the person asking is rarely the account's owner.
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

// CompleteReset spends a token and sets the password, in ONE transaction.
//
// Everything is in that transaction for a reason:
//
//   - Spending and setting together, because spending first would burn the
//     token on a password that never changed — the customer is locked out AND
//     their one link is gone.
//   - Every OTHER unused token for the account is invalidated too. Somebody who
//     clicked "forgot password" three times has two more live links sitting in
//     their mailbox, and a mailbox is exactly what an attacker reads.
//   - Every session ends, including the attacker's. If somebody got in with the
//     old password, a reset that leaves their session alive has changed
//     nothing.
func (s *Store) CompleteReset(ctx context.Context, token, password string) error {
	if why := PasswordError(password); why != "" {
		return fmt.Errorf("%w: %s", ErrInvalidPassword, why)
	}
	digest := sha256.Sum256([]byte(token))

	// A cheap gate BEFORE argon2. Hashing first would let anybody spend 64 MiB
	// and 28 ms of this process per garbage token they invent — and the rate
	// limiter is keyed on the token, so inventing a new one buys a fresh
	// allowance. The authoritative check is the UPDATE below; this one only
	// decides whether the expensive work is worth starting.
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

	// The token is spent HERE, under its own WHERE clause. Two requests
	// carrying the same token both reach this line; only one updates a row, and
	// the loser is refused. A check in Go is a check they both pass, and the
	// prize is somebody else's account.
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
//
// A plain enqueue and not a transaction with the token's creation: the token is
// already committed, and a message lost between the two is a customer who asks
// again. The reverse — a message sent for a token that rolled back — would be a
// link that does nothing, which is worse.
func (s *Store) EnqueueReset(ctx context.Context, email, token string) error {
	digest := sha256.Sum256([]byte(token))
	payload, err := json.Marshal(email2.PasswordReset{
		Email: email, Token: token, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("encode reset message: %w", err)
	}
	if err := s.q.EnqueueMessage(ctx, db.EnqueueMessageParams{
		Topic: outbox.TopicPasswordReset,
		// Keyed on the token, so a retried enqueue is one message — and each
		// new request has its own token and therefore its own mail.
		DedupeKey: "reset:" + hex.EncodeToString(digest[:]),
		Payload:   payload,
	}); err != nil {
		return fmt.Errorf("enqueue reset message: %w", err)
	}
	return nil
}
