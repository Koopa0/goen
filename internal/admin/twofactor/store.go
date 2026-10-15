package twofactor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

type Store struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	cipher *secretCipher
}

// NewStore seals new secrets under key and opens them under key or, while a key
// is being rotated out, previous. Both come from [ParseKey]; previous may be
// nil, and key nil disables the store.
func NewStore(pool *pgxpool.Pool, key, previous []byte) *Store {
	if pool == nil {
		panic("twofactor: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool), cipher: newCipher(key, previous)}
}

func (s *Store) Enabled() bool { return s.cipher.enabled() }

// Begin starts enrolment and returns the secret to show once. The second code
// [Store.Confirm] needs goes to address by mail, never to the page. The
// credential is not confirmed here, and an already-confirmed one is refused
// with [ErrEnrolled] — recovery is another admin removing it.
func (s *Store) Begin(ctx context.Context, userID, address string) (secret []byte, uri string, err error) {
	if !s.Enabled() {
		return nil, "", ErrDisabled
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", ErrNotEnrolled
	}
	secret, err = NewSecret()
	if err != nil {
		return nil, "", err
	}
	sealed, err := s.cipher.seal(secret, id)
	if err != nil {
		return nil, "", err
	}
	mailed, err := newMailedCode()
	if err != nil {
		return nil, "", err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin totp enrolment: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	n, err := q.BeginTOTPEnrolment(ctx, db.BeginTOTPEnrolmentParams{
		UserID: id, SecretEncrypted: sealed, MailedCodeDigest: mailedCodeHash(mailed),
	})
	if err != nil {
		return nil, "", fmt.Errorf("begin totp enrolment: %w", err)
	}
	if n == 0 {
		return nil, "", ErrEnrolled
	}
	if err := outbox.Enqueue(ctx, q, outbox.TopicStaffEnrolment, "staff-enrolment:"+uuid.NewString(), &email.StaffEnrolment{
		Locale: i18n.FromContext(ctx).Tag(), Email: address, Code: mailed,
	}); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit totp enrolment: %w", err)
	}
	return secret, ProvisioningURI(address, secret), nil
}

// Confirm proves enrolment with a code from the new authenticator and the code
// [Store.Begin] mailed. A wrong, expired or superseded mailed code is
// [ErrBadCode].
func (s *Store) Confirm(ctx context.Context, userID, code, mailedCode string) error {
	cred, err := s.load(ctx, userID, false)
	if err != nil {
		return err
	}
	step, err := Verify(cred.secret, code, cred.lastStep, time.Now())
	if err != nil {
		return err
	}
	n, err := s.q.ConfirmTOTP(ctx, db.ConfirmTOTPParams{
		UserID: cred.id, Step: step, SecretEncrypted: cred.sealed,
		MailedCodeDigest: mailedCodeHash(mailedCode),
		MailedCodeTtl:    pgtype.Interval{Microseconds: MailedCodeTTL.Microseconds(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("confirm totp: %w", err)
	}
	if n == 0 {
		// The mailed code is wrong or expired, a concurrent request used this
		// same code first, or enrolment restarted with another secret.
		return ErrBadCode
	}
	return nil
}

// Verify checks a code for a confirmed credential and records the step. The
// step is recorded by the same statement that guards it, so a check made in Go
// would be a check two requests replaying one code both pass.
//
// A secret the current key did not open is sealed again under the current key
// in that same statement, so a key rotation finishes as staff sign in. Only an
// accepted code re-seals: a refused one proves nothing about who is asking.
func (s *Store) Verify(ctx context.Context, userID, code string) error {
	cred, err := s.load(ctx, userID, true)
	if err != nil {
		return err
	}
	step, err := Verify(cred.secret, code, cred.lastStep, time.Now())
	if err != nil {
		return err
	}
	var resealed []byte
	if !cred.current {
		resealed, err = s.cipher.seal(cred.secret, cred.id)
		if err != nil {
			return fmt.Errorf("reseal totp secret: %w", err)
		}
	}
	n, err := s.q.RecordTOTPStep(ctx, db.RecordTOTPStepParams{UserID: cred.id, Step: step, Resealed: resealed})
	if err != nil {
		return fmt.Errorf("record totp step: %w", err)
	}
	if n == 0 {
		return ErrBadCode
	}
	return nil
}

func (s *Store) Enrolled(ctx context.Context, userID string) (bool, error) {
	_, err := s.load(ctx, userID, true)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotEnrolled):
		return false, nil
	default:
		return false, err
	}
}

// MarkVerified records that the session named by digest passed the second
// factor.
func (s *Store) MarkVerified(ctx context.Context, digest []byte) error {
	if err := s.q.MarkSessionVerified(ctx, digest); err != nil {
		return fmt.Errorf("mark session verified: %w", err)
	}
	return nil
}

func (s *Store) SessionVerified(ctx context.Context, digest []byte) (bool, error) {
	verified, err := s.q.SessionTOTPVerified(ctx, db.SessionTOTPVerifiedParams{
		Digest: digest,
		MaxAge: pgtype.Interval{Microseconds: StepUpWindow.Microseconds(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read session verification: %w", err)
	}
	return verified, nil
}

// credential is a stored credential after decryption.
type credential struct {
	id       uuid.UUID
	secret   []byte
	lastStep int64
	// sealed is the stored form the secret was read from, which a write must
	// name so it lands on that secret and no other.
	sealed []byte
	// current is whether the current key opened sealed.
	current bool
}

// load reads and decrypts a credential. Confirm is the one caller for which an
// unproved secret is legitimately in play; everyone else passes requireConfirmed.
func (s *Store) load(ctx context.Context, userID string, requireConfirmed bool) (credential, error) {
	if !s.Enabled() {
		return credential{}, ErrDisabled
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return credential{}, ErrNotEnrolled
	}
	row, err := s.q.TOTPCredential(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return credential{}, ErrNotEnrolled
		}
		return credential{}, fmt.Errorf("read totp credential: %w", err)
	}
	if requireConfirmed && !row.ConfirmedAt.Valid {
		return credential{}, ErrNotEnrolled
	}
	secret, current, err := s.cipher.open(row.SecretEncrypted, id)
	if err != nil {
		return credential{}, fmt.Errorf("%w: %w", ErrSecretUnreadable, err)
	}
	return credential{id: id, secret: secret, lastStep: row.LastStep.Int64, sealed: row.SecretEncrypted, current: current}, nil
}

// newMailedCode is eight digits rather than six: the enrolling page already
// shows the secret, so this code alone stands against guesses at the rate
// Handler.limit allows for [MailedCodeTTL].
func newMailedCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		return "", fmt.Errorf("generate mailed code: %w", err)
	}
	return fmt.Sprintf("%08d", n), nil
}

func mailedCodeHash(code string) []byte {
	digest := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return digest[:]
}
