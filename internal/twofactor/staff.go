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
// The bool is an OUTCOME, not an error: the promotion SUCCEEDED, and what the
// caller has to relay is that the address already had an account which had
// never proved the mailbox, so its password was cleared and its sessions ended.
// Carrying that as a sentinel made every `if err != nil` in the chain read a
// success as a failure.
func (s *Store) AddStaff(ctx context.Context, address, name, role, actorID string) (bool, error) {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	if !email.Valid(address) || !contains(Roles, role) {
		return false, ErrInvalidStaff
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return false, ErrInvalidStaff
	}
	// Before the write: afterwards there is nothing to compare but the row the
	// upsert already changed.
	self, err := s.q.UserHasEmail(ctx, db.UserHasEmailParams{ID: actor, Email: address})
	if err != nil {
		return false, fmt.Errorf("check the actor's own address: %w", err)
	}
	if self {
		return false, ErrSelf
	}
	row, err := s.q.UpsertStaff(ctx, db.UpsertStaffParams{
		Email: address, FullName: name, Role: role,
	})
	if err != nil {
		return false, fmt.Errorf("add staff %s: %w", address, err)
	}
	// Said rather than swallowed: the person being hired now has no way in until
	// they set a password through /forgot, and the admin is the one who has to
	// tell them.
	return row.CredentialCleared, nil
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

	n, err := s.q.RevokeStaff(ctx, target)
	if err != nil {
		return fmt.Errorf("revoke staff: %w", err)
	}
	if n == 0 {
		// The statement asks the last-admin question under FOR UPDATE, so this
		// is where it is answered — and it answers two questions at once.
		// A Go pre-check would give a nicer message and would make the real
		// guard almost unreachable, which is how a redundant check comes to be
		// the only one anybody has watched work.
		return s.whyRevokeMatchedNothing(ctx, target)
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

// whyRevokeMatchedNothing turns a zero row count into the sentence the staff
// member needs: the target was not staff at all, or it was the last admin.
func (s *Store) whyRevokeMatchedNothing(ctx context.Context, target uuid.UUID) error {
	rows, err := s.q.StaffTOTPStatus(ctx)
	if err != nil {
		return fmt.Errorf("read staff: %w", err)
	}
	for i := range rows {
		if rows[i].ID == target && rows[i].Role == "admin" {
			return ErrLastAdmin
		}
	}
	return ErrInvalidStaff
}

func contains(all []string, want string) bool { return slices.Contains(all, want) }
