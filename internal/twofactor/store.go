package twofactor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
)

type Store struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	cipher *secretCipher
}

func NewStore(pool *pgxpool.Pool, key []byte) *Store {
	if pool == nil {
		panic("twofactor: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool), cipher: newCipher(key)}
}

func (s *Store) Enabled() bool { return s.cipher.enabled() }

// Begin starts enrolment and returns the secret to show once. The credential is
// not confirmed here, and an already-confirmed one is refused with
// [ErrEnrolled] — recovery is another admin removing it.
func (s *Store) Begin(ctx context.Context, userID, email string) (secret []byte, uri string, err error) {
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
	sealed, err := s.cipher.seal(secret)
	if err != nil {
		return nil, "", err
	}
	n, err := s.q.BeginTOTPEnrolment(ctx, db.BeginTOTPEnrolmentParams{
		UserID: id, SecretEncrypted: sealed,
	})
	if err != nil {
		return nil, "", fmt.Errorf("begin totp enrolment: %w", err)
	}
	if n == 0 {
		return nil, "", ErrEnrolled
	}
	return secret, ProvisioningURI(email, secret), nil
}

func (s *Store) Confirm(ctx context.Context, userID, code string) error {
	id, secret, lastStep, sealed, err := s.load(ctx, userID, false)
	if err != nil {
		return err
	}
	step, err := Verify(secret, code, lastStep, time.Now())
	if err != nil {
		return err
	}
	n, err := s.q.ConfirmTOTP(ctx, db.ConfirmTOTPParams{UserID: id, Step: step, SecretEncrypted: sealed})
	if err != nil {
		return fmt.Errorf("confirm totp: %w", err)
	}
	if n == 0 {
		// A concurrent request used this same code first, or restarted
		// enrolment with another secret.
		return ErrBadCode
	}
	return nil
}

// Verify checks a code for a confirmed credential and records the step. The
// step is recorded by the same statement that guards it, so a check made in Go
// would be a check two requests replaying one code both pass.
func (s *Store) Verify(ctx context.Context, userID, code string) error {
	id, secret, lastStep, _, err := s.load(ctx, userID, true)
	if err != nil {
		return err
	}
	step, err := Verify(secret, code, lastStep, time.Now())
	if err != nil {
		return err
	}
	n, err := s.q.RecordTOTPStep(ctx, db.RecordTOTPStepParams{UserID: id, Step: step})
	if err != nil {
		return fmt.Errorf("record totp step: %w", err)
	}
	if n == 0 {
		return ErrBadCode
	}
	return nil
}

func (s *Store) Enrolled(ctx context.Context, userID string) (bool, error) {
	_, _, _, _, err := s.load(ctx, userID, true)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotEnrolled):
		return false, nil
	default:
		return false, err
	}
}

func (s *Store) MarkVerified(ctx context.Context, token string) error {
	digest := sha256.Sum256([]byte(token))
	if err := s.q.MarkSessionVerified(ctx, digest[:]); err != nil {
		return fmt.Errorf("mark session verified: %w", err)
	}
	return nil
}

func (s *Store) SessionVerified(ctx context.Context, token string) (bool, error) {
	digest := sha256.Sum256([]byte(token))
	verified, err := s.q.SessionTOTPVerified(ctx, db.SessionTOTPVerifiedParams{
		TokenHash: digest[:],
		MaxAge:    pgtype.Interval{Microseconds: StepUpWindow.Microseconds(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read session verification: %w", err)
	}
	return verified, nil
}

// load reads and decrypts a credential; sealed is the stored form the secret was
// read from, which a write must name so it lands on that secret and no other. Confirm is the one caller for which an
// unproved secret is legitimately in play; everyone else passes requireConfirmed.
func (s *Store) load(ctx context.Context, userID string, requireConfirmed bool) (id uuid.UUID, secret []byte, lastStep int64, sealed []byte, err error) {
	if !s.Enabled() {
		return uuid.UUID{}, nil, 0, nil, ErrDisabled
	}
	id, err = uuid.Parse(userID)
	if err != nil {
		return uuid.UUID{}, nil, 0, nil, ErrNotEnrolled
	}
	row, err := s.q.TOTPCredential(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, nil, 0, nil, ErrNotEnrolled
		}
		return uuid.UUID{}, nil, 0, nil, fmt.Errorf("read totp credential: %w", err)
	}
	if requireConfirmed && !row.ConfirmedAt.Valid {
		return uuid.UUID{}, nil, 0, nil, ErrNotEnrolled
	}
	secret, err = s.cipher.open(row.SecretEncrypted)
	if err != nil {
		return uuid.UUID{}, nil, 0, nil, fmt.Errorf("%w: %w", ErrSecretUnreadable, err)
	}
	return id, secret, row.LastStep.Int64, row.SecretEncrypted, nil
}
