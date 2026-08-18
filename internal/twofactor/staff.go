package twofactor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

// Errors the staff page branches on.
var (
	// ErrLastAdmin is a change that would leave nobody able to make another.
	ErrLastAdmin = errors.New("twofactor: that would leave no admin")
	// ErrSelf is an admin acting on their own account. The session doing it is
	// already step-up verified, so self-service would turn a stolen session into
	// permanent access.
	ErrSelf = errors.New("twofactor: an admin cannot do that to their own account")
	// ErrInvalidStaff is a form the rules refuse.
	ErrInvalidStaff = errors.New("twofactor: that is not a usable staff account")

	// ErrCredentialCleared is a SUCCESS the admin has to be told about. The
	// address already had an account that had never proved the mailbox, so its
	// password was cleared and its sessions ended; the new colleague sets one
	// through /forgot, exactly as one created from scratch does.
	ErrCredentialCleared = errors.New("twofactor: the promoted account had not proved its address")
)

// Roles a staff account may hold, in the order the form offers them.
var Roles = []string{"staff", "admin"}

// RoleLabel is what a role is called.
func RoleLabel(ctx context.Context, role string) string {
	switch role {
	case "staff":
		return i18n.T(ctx, i18n.KeyAdminRoleStaff)
	case "admin":
		return i18n.T(ctx, i18n.KeyAdminRoleAdmin)
	default:
		panic("twofactor: no label for role " + role)
	}
}

// AddStaff gives somebody back-office access, with no password set. It is an
// UPSERT resolved by address, so submitting your own address is a
// self-promotion; the actor is compared for that reason.
func (s *Store) AddStaff(ctx context.Context, address, name, role, actorID string) error {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	if !email.Valid(address) || !contains(Roles, role) {
		return ErrInvalidStaff
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ErrInvalidStaff
	}
	// Before the write: afterwards there is nothing to compare but the row the
	// upsert already changed.
	self, err := s.q.UserHasEmail(ctx, db.UserHasEmailParams{ID: actor, Email: address})
	if err != nil {
		return fmt.Errorf("check the actor's own address: %w", err)
	}
	if self {
		return ErrSelf
	}
	row, err := s.q.UpsertStaff(ctx, db.UpsertStaffParams{
		Email: address, FullName: name, Role: role,
	})
	if err != nil {
		return fmt.Errorf("add staff %s: %w", address, err)
	}
	if row.CredentialCleared {
		// Said rather than swallowed: the person being hired now has no way in
		// until they set a password through /forgot, and the admin is the one
		// who has to tell them.
		return ErrCredentialCleared
	}
	return nil
}

// RevokeStaff takes back-office access away, and ends every session that had it.
func (s *Store) RevokeStaff(ctx context.Context, userID, actorID string) error {
	target, err := uuid.Parse(userID)
	if err != nil {
		return ErrInvalidStaff
	}
	if userID == actorID {
		return ErrSelf
	}
	if guardErr := s.guardLastAdmin(ctx, target); guardErr != nil {
		return guardErr
	}

	n, err := s.q.RevokeStaff(ctx, target)
	if err != nil {
		return fmt.Errorf("revoke staff: %w", err)
	}
	if n == 0 {
		return ErrInvalidStaff
	}
	// Without this they keep the back office for the rest of a session's life.
	if err := s.q.EndStaffSessions(ctx, target); err != nil {
		return fmt.Errorf("end staff sessions: %w", err)
	}
	return nil
}

// RemoveFactor deletes somebody else's second factor, which is how a lost
// authenticator is recovered.
func (s *Store) RemoveFactor(ctx context.Context, userID, actorID string) error {
	if userID == actorID {
		return ErrSelf
	}
	return s.Remove(ctx, userID)
}

// guardLastAdmin refuses a change that would leave nobody able to make another.
func (s *Store) guardLastAdmin(ctx context.Context, target uuid.UUID) error {
	admins, err := s.q.CountAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if admins > 1 {
		return nil
	}
	rows, err := s.q.StaffTOTPStatus(ctx)
	if err != nil {
		return fmt.Errorf("read staff: %w", err)
	}
	for i := range rows {
		if rows[i].ID == target && rows[i].Role == "admin" {
			return ErrLastAdmin
		}
	}
	return nil
}

func contains(all []string, want string) bool { return slices.Contains(all, want) }
