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
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store is the database side of the second factor.
type Store struct {
	q      *db.Queries
	cipher *Cipher
}

// NewStore returns a Store over pool, encrypting with key.
func NewStore(pool *pgxpool.Pool, key string) *Store {
	if pool == nil {
		panic("twofactor: NewStore requires a pool")
	}
	return &Store{q: db.New(pool), cipher: NewCipher(key)}
}

// Enabled reports whether enrolment is possible in this deployment.
func (s *Store) Enabled() bool { return s.cipher.Enabled() }

// Begin starts enrolment and returns the secret to show once.
//
// Shown once and never again: after this response the plaintext exists only in
// the enrolling person's authenticator. A page that could redisplay it would be
// a page an attacker with a stolen session could read.
//
// The credential is NOT confirmed here. Somebody who mistypes the secret into
// their app would otherwise have working 2FA on paper and no way to produce a
// code, which locks them out of exactly the thing 2FA is protecting.
//
// A credential that is already CONFIRMED is refused with [ErrEnrolled]. This
// route is reached with a password and an ordinary session, so overwriting a
// proved factor here would mean a stolen password is the whole back office:
// enrol your own authenticator over theirs, confirm it, and the session is
// step-up verified. Recovery is another admin removing the credential.
// The account label is the EMAIL, not the user id: it is what an authenticator
// app shows beside the code, and a person with two accounts needs to tell them
// apart. A UUID there is unreadable and identical-looking to every other one.
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
	sealed, err := s.cipher.Seal(secret)
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
		// The statement's own WHERE refused: this credential is already proved,
		// and replacing it needs more than the password that got us here.
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
		// The step guard refused, which means a concurrent request used this
		// same code first.
		return ErrBadCode
	}
	return nil
}

// Verify checks a code for a confirmed credential and records the step.
//
// The step is recorded by the same statement that guards it, so two requests
// replaying one code cannot both win — a check made in Go would be a check both
// of them pass.
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

// Remove deletes a credential, which is how a lost authenticator is recovered.
//
// Another admin does this; there are no backup codes. That is a deliberate
// trade: printed codes are a second password-equivalent that people keep in
// their email, and a shop with more than one admin has a recovery path already.
func (s *Store) Remove(ctx context.Context, userID string) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotEnrolled
	}
	n, err := s.q.RemoveTOTP(ctx, id)
	if err != nil {
		return fmt.Errorf("remove totp: %w", err)
	}
	if n == 0 {
		return ErrNotEnrolled
	}
	return nil
}

// load reads and decrypts a credential.
//
// requireConfirmed is what separates the two callers. Confirm is the one moment
// an unproved secret is legitimately in play — it is proving it — and everyone
// else must treat an unconfirmed credential as absent.
//
// It is a parameter enforced here and not a rule each caller remembers, because
// a credential that reports itself enrolled while unconfirmed locks a
// half-finished enrolment out of exactly what it protects: the challenge page
// asks for a code the person has no way to generate, and there is no route back
// to enrolment.
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
	secret, err = s.cipher.Open(row.SecretEncrypted)
	if err != nil {
		return uuid.UUID{}, nil, 0, err
	}
	return id, secret, row.LastStep.Int64, nil
}

// Staff reads who can reach the back office and who is protected.
//
// Enrolment is voluntary — nothing forces a staff member to set up a second
// factor — so "who has it on" is the question a shop owner has to be able to
// ask, and StaffTOTPStatus is the answer /admin/staff renders.
func (s *Store) Staff(ctx context.Context) (pages.AdminStaffView, error) {
	rows, err := s.q.StaffTOTPStatus(ctx)
	if err != nil {
		return pages.AdminStaffView{}, fmt.Errorf("read staff 2FA status: %w", err)
	}
	view := pages.AdminStaffView{Rows: make([]pages.AdminStaffRow, 0, len(rows))}
	for _, role := range Roles {
		view.Roles = append(view.Roles, pages.StaffRoleChoice{
			Value: role, Label: RoleLabel(ctx, role),
		})
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminStaffRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Role: r.Role, Enrolled: r.Enrolled,
		})
	}
	return view, nil
}
