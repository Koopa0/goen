package twofactor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store is the database side of the second factor.
type Store struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	cipher *secretCipher
}

// NewStore returns a Store over pool, encrypting with parsed key material.
func NewStore(pool *pgxpool.Pool, key []byte) *Store {
	if pool == nil {
		panic("twofactor: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool), cipher: newCipher(key)}
}

// Enabled reports whether enrolment is possible in this deployment.
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

// Confirm finishes enrolment by checking a code the person generated.
func (s *Store) Confirm(ctx context.Context, userID, code string) error {
	id, secret, lastStep, err := s.load(ctx, userID, false)
	if err != nil {
		return err
	}
	step, err := Verify(secret, code, lastStep, time.Now())
	if err != nil {
		return err
	}
	n, err := s.q.ConfirmTOTP(ctx, db.ConfirmTOTPParams{UserID: id, Step: step})
	if err != nil {
		return fmt.Errorf("confirm totp: %w", err)
	}
	if n == 0 {
		// A concurrent request used this same code first.
		return ErrBadCode
	}
	return nil
}

// Verify checks a code for a confirmed credential and records the step. The
// step is recorded by the same statement that guards it, so a check made in Go
// would be a check two requests replaying one code both pass.
func (s *Store) Verify(ctx context.Context, userID, code string) error {
	id, secret, lastStep, err := s.load(ctx, userID, true)
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

// Enrolled reports whether this user has a confirmed credential.
func (s *Store) Enrolled(ctx context.Context, userID string) (bool, error) {
	_, _, _, err := s.load(ctx, userID, true)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotEnrolled):
		return false, nil
	default:
		return false, err
	}
}

// MarkVerified records that this session proved a second factor.
func (s *Store) MarkVerified(ctx context.Context, token string) error {
	digest := sha256.Sum256([]byte(token))
	if err := s.q.MarkSessionVerified(ctx, digest[:]); err != nil {
		return fmt.Errorf("mark session verified: %w", err)
	}
	return nil
}

// SessionVerified reports whether this session's proof is still current.
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

// load reads and decrypts a credential. Confirm is the one caller for which an
// unproved secret is legitimately in play; everyone else passes requireConfirmed.
func (s *Store) load(ctx context.Context, userID string, requireConfirmed bool) (id uuid.UUID, secret []byte, lastStep int64, err error) {
	if !s.Enabled() {
		return uuid.UUID{}, nil, 0, ErrDisabled
	}
	id, err = uuid.Parse(userID)
	if err != nil {
		return uuid.UUID{}, nil, 0, ErrNotEnrolled
	}
	row, err := s.q.TOTPCredential(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, nil, 0, ErrNotEnrolled
		}
		return uuid.UUID{}, nil, 0, fmt.Errorf("read totp credential: %w", err)
	}
	if requireConfirmed && !row.ConfirmedAt.Valid {
		return uuid.UUID{}, nil, 0, ErrNotEnrolled
	}
	secret, err = s.cipher.open(row.SecretEncrypted)
	if err != nil {
		return uuid.UUID{}, nil, 0, fmt.Errorf("%w: %w", ErrSecretUnreadable, err)
	}
	return id, secret, row.LastStep.Int64, nil
}

// Staff reads who can reach the back office and who is protected.
func (s *Store) Staff(ctx context.Context) (pages.AdminStaffView, error) {
	rows, err := s.q.StaffTOTPStatus(ctx)
	if err != nil {
		return pages.AdminStaffView{}, fmt.Errorf("read staff 2FA status: %w", err)
	}
	view := pages.AdminStaffView{Rows: make([]pages.AdminStaffRow, 0, len(rows))}
	// Cloned: a slice of the package-level array would let a caller write through it.
	view.Roles = slices.Clone(pages.StaffRoles[:])
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminStaffRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Role: pages.StaffRole(r.Role), Enrolled: r.Enrolled,
		})
	}
	return view, nil
}
