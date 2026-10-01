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

// ErrVerifyNeedsPassword is a link that would complete a registration, followed
// without the password chosen at registration. It is left unspent.
var ErrVerifyNeedsPassword = errors.New("account: completing a registration takes its password")

// ErrVerifyNeedsSignIn is a link that would move an account to a new address,
// followed from a browser signed in to no account. It is left unspent.
var ErrVerifyNeedsSignIn = errors.New("account: proving a new address takes the account that asked for it")

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
// the queued link is followed. It does the same work whether or not addr is
// another account's, so the caller can answer both the same; the outbox worker
// decides what the mailbox is sent, in [Store.DeliverAddressVerify].
func (s *Store) requestVerification(ctx context.Context, userID, addr string) error {
	return s.queueVerification(ctx, userID, addr, email.AddressVerify{})
}

// queueVerification is requestVerification for a link shaped by link: its
// Registration and Next are kept, and its address, token and locale are set
// here.
func (s *Store) queueVerification(ctx context.Context, userID, addr string, link email.AddressVerify) error {
	id, parseErr := uuid.Parse(userID)
	if parseErr != nil {
		return fmt.Errorf("parse user id: %w", parseErr)
	}
	addr = email.Clean(addr)
	if EmailError(addr) != "" {
		return fmt.Errorf("requesting verification of %q: not a usable address", addr)
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

	link.Email, link.Token, link.Locale = addr, token, i18n.FromContext(ctx).Tag()
	payload, marshalErr := json.Marshal(link)
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

// DeliverAddressVerify is the outbox's half of a request to prove an address.
// The link goes out through send, unless by now the address is another
// account's: then that account is told through tell, at the address it holds,
// and the link goes nowhere. The request answered both the same, so only the
// mailbox learns which it was. A link already spent, expired or replaced is
// nothing to send.
func (s *Store) DeliverAddressVerify(
	ctx context.Context,
	p *email.AddressVerify,
	send func(context.Context, *email.AddressVerify) error,
	tell func(context.Context, *email.AccountExists) error,
) error {
	verification, err := usableVerification(ctx, s.q, HashToken(p.Token))
	if errors.Is(err, ErrVerifyInvalid) {
		return nil
	}
	if err != nil {
		return err
	}
	holder, err := s.q.OtherAccountAtAddress(ctx, db.OtherAccountAtAddressParams{
		Email: verification.Email, UserID: verification.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return send(ctx, p)
	}
	if err != nil {
		return fmt.Errorf("read the account at the address: %w", err)
	}
	return tell(ctx, &email.AccountExists{
		Locale: p.Locale, Email: holder.Email, Name: holder.FullName.String, Change: true,
	})
}

// Confirmed is what following a verification link did.
type Confirmed struct {
	// Email is the address now proved.
	Email string
	// UserID is the account it belongs to.
	UserID string
}

// ConfirmVerification spends a link, moves the address and marks it proved,
// for the account userID is signed in to, or "" for none. The link reaches the
// mailbox and nothing says the mailbox's owner is the account that asked, so
// it proves the address only for that account, signed in: followed from no
// account it is ErrVerifyNeedsSignIn, and from another account ErrVerifyInvalid,
// the answer to a dead link, both left unspent. A link that would complete a
// registration is refused with ErrVerifyNeedsPassword and left unspent: that
// takes CompleteRegistration.
func (s *Store) ConfirmVerification(ctx context.Context, token, userID string) (Confirmed, error) {
	var asker uuid.NullUUID
	if userID != "" {
		id, err := uuid.Parse(userID)
		if err != nil {
			return Confirmed{}, fmt.Errorf("parse user id: %w", err)
		}
		asker = uuid.NullUUID{UUID: id, Valid: true}
	}
	return s.confirm(ctx, token, asker, false)
}

// RegistrationAddress is the address a registration link would prove, read
// without spending it, or ErrVerifyInvalid.
func (s *Store) RegistrationAddress(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrVerifyInvalid
	}
	verification, err := usableVerification(ctx, s.q, HashToken(token))
	if err != nil {
		return "", err
	}
	return verification.Email, nil
}

// CompleteRegistration spends a registration link, proves the address and
// makes the account usable, when password is the one chosen at registration.
// The link reaches the mailbox and the password was chosen by whoever
// registered; only together are they the registrant. Anything else leaves the
// account unproved: a wrong password is ErrBadCredentials, at the cost of the
// same hash sign-in makes, and a link that completes no registration is
// ErrVerifyInvalid.
func (s *Store) CompleteRegistration(ctx context.Context, token, password string) (Confirmed, error) {
	if len(password) > MaxPasswordBytes {
		return Confirmed{}, ErrBadCredentials
	}
	if token == "" {
		return Confirmed{}, ErrVerifyInvalid
	}
	verification, err := usableVerification(ctx, s.q, HashToken(token))
	if err != nil {
		return Confirmed{}, err
	}
	credential, err := s.q.RegistrationCredential(ctx, verification.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Confirmed{}, ErrVerifyInvalid
	}
	if err != nil {
		return Confirmed{}, fmt.Errorf("read the registered credential: %w", err)
	}
	if !passwordMatches(credential, password) {
		return Confirmed{}, ErrBadCredentials
	}
	return s.confirm(ctx, token, uuid.NullUUID{}, true)
}

// confirm spends a link inside one transaction. completing says whether the
// caller has proved it is the registrant: without that, a link that would
// complete a registration is refused; with it, any other link is. Any other
// link is spent only for asker, the signed-in account that asked for it.
func (s *Store) confirm(ctx context.Context, token string, asker uuid.NullUUID, completing bool) (Confirmed, error) {
	if token == "" {
		return Confirmed{}, ErrVerifyInvalid
	}
	digest := HashToken(token)
	verification, err := usableVerification(ctx, s.q, digest)
	if err != nil {
		return Confirmed{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Confirmed{}, fmt.Errorf("begin verification: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	lockedUser, err := lockVerificationAccount(ctx, q, verification.UserID)
	if err != nil {
		return Confirmed{}, err
	}
	// Read under the lock: a reset spent in between proves the address itself.
	registration := !lockedUser.Verified && strings.EqualFold(lockedUser.Email, verification.Email)
	if refusal := linkRefusal(registration, completing, asker, verification.UserID); refusal != nil {
		return Confirmed{}, refusal
	}
	row, err := spendMatchingVerification(ctx, q, digest, verification)
	if err != nil {
		return Confirmed{}, err
	}

	if err := setVerifiedEmail(ctx, q, row); err != nil {
		return Confirmed{}, err
	}
	// A reset link was sent to the old address. Once that address no longer
	// identifies this account, its holder must not be able to choose a password.
	if err := retireOldMailboxResets(ctx, q, lockedUser.Email, row); err != nil {
		return Confirmed{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Confirmed{}, fmt.Errorf("commit verification: %w", err)
	}
	return Confirmed{Email: row.Email, UserID: row.UserID.String()}, nil
}

// linkRefusal is why a link owned by owner may not be spent by this caller, or
// nil. A registration link is spent only by its registrant, completing; any
// other link only by asker, the account that asked for it, signed in.
func linkRefusal(registration, completing bool, asker uuid.NullUUID, owner uuid.UUID) error {
	switch {
	case registration && !completing:
		return ErrVerifyNeedsPassword
	case registration:
		return nil
	case completing:
		return ErrVerifyInvalid
	case !asker.Valid:
		return ErrVerifyNeedsSignIn
	case asker.UUID != owner:
		// The dead-link answer: another account learns nothing about the link.
		return ErrVerifyInvalid
	}
	return nil
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
