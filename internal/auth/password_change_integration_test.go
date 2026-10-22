//go:build integration

package auth_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
)

// TestStore_ChangePasswordEndsSessions: ChangePassword ends the account's
// sessions, so a stolen session does not outlive the password change meant to
// end it, and the new password signs in while the old one does not.
func TestStore_ChangePasswordEndsSessions(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := registerProved(t, s, "rotate@example.com")

	stolen, err := s.StartSession(ctx, u.ID, "thief", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); err != nil {
		t.Fatalf("the session did not start: %v", err)
	}

	if err := s.ChangePassword(ctx, u.ID, "an entirely different password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); !errors.Is(err, auth.ErrNotFound) {
		t.Error("a session survived the password change that was meant to end it")
	}
	if _, err := s.Authenticate(ctx, "rotate@example.com", "an entirely different password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if _, err := s.Authenticate(ctx, "rotate@example.com", "a sufficiently long password"); err == nil {
		t.Error("the old password still works")
	}
}

// TestStore_ChangePasswordResetTokens: a reset token issued before
// ChangePassword no longer completes (ErrResetInvalid), so a stale reset link
// cannot overwrite the password chosen in account settings.
func TestStore_ChangePasswordResetTokens(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "rotate-reset-" + uuid.NewString() + "@example.com"
	u := registerProved(t, s, email)

	token := beginReset(t, s, email)
	if err := s.ChangePassword(ctx, u.ID, "an entirely different password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := s.CompleteReset(ctx, token, "password the stale link tried to set"); !errors.Is(err, auth.ErrResetInvalid) {
		t.Errorf("a reset token issued before the password change still works: %v", err)
	}
	if _, err := s.Authenticate(ctx, email, "password the stale link tried to set"); err == nil {
		t.Error("the stale reset link changed the password anyway")
	}
	if _, err := s.Authenticate(ctx, email, "an entirely different password"); err != nil {
		t.Errorf("the password chosen in account settings does not work: %v", err)
	}
}
