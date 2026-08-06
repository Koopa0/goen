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
)

// Errors the staff page branches on.
var (
	// ErrLastAdmin is the guard that stops the back office being locked shut.
	// Removing the only admin leaves nobody who can add one back, and the fix
	// is SQL against production — which is exactly what a back office exists to
	// avoid.
	ErrLastAdmin = errors.New("twofactor: that would leave no admin")
	// ErrSelf is an admin acting on their own account.
	//
	// Removing your OWN second factor is the interesting one: your session is
	// already step-up verified, so an attacker holding it could drop the factor
	// and re-enrol on their own device. Recovery is another admin's job, which
	// is what the design says and now what the code enforces.
	ErrSelf = errors.New("twofactor: an admin cannot do that to their own account")
	// ErrInvalidStaff is a form the rules refuse.
	ErrInvalidStaff = errors.New("twofactor: that is not a usable staff account")
)

// Roles a staff account may hold, in the order the form offers them.
var Roles = []string{"staff", "admin"}

// RoleLabel is what a role is called. No silent default: a role added to the
// schema's CHECK and not here would render as an empty option.
func RoleLabel(role string) string {
	switch role {
	case "staff":
		return "員工"
	case "admin":
		return "管理員"
	default:
		panic("twofactor: no label for role " + role)
	}
}

// AddStaff gives somebody back-office access.
//
// No password is set. An admin who typed a colleague's password would know it,
// and a generated one has to be delivered somehow — so the colleague sets their
// own through /forgot, which is already the one path that proves they own the
// mailbox. The account cannot be signed into until they do.
//
// The ACTOR is passed and compared, and that is not ceremony: this is an UPSERT,
// so submitting an address that already has an account changes that account's
// ROLE. Typing your own address is therefore a promotion, and it was the whole
// escalation — a staff member POSTing their own email with role=admin. The route
// is admin-only now, which closes the staff case; this closes the one the route
// cannot see, where an admin edits their own row.
//
// It is refused rather than silently ignored: an admin who meant to change their
// own name and got no error would reasonably believe it had worked.
func (s *Store) AddStaff(ctx context.Context, address, name, role, actorID string) error {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	if !email.Valid(address) || !contains(Roles, role) {
		return ErrInvalidStaff
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ErrInvalidStaff
	}
	// Asked BEFORE the write and by address, because the upsert resolves the
	// account by address and there is nothing to compare afterwards but the row
	// it already changed.
	self, err := s.q.UserHasEmail(ctx, db.UserHasEmailParams{ID: actor, Email: address})
	if err != nil {
		return fmt.Errorf("check the actor's own address: %w", err)
	}
	if self {
		return ErrSelf
	}
	if _, err := s.q.UpsertStaff(ctx, db.UpsertStaffParams{
		Email: address, FullName: name, Role: role,
	}); err != nil {
		return fmt.Errorf("add staff %s: %w", address, err)
	}
	return nil
}

// RevokeStaff takes back-office access away, and ends every session that had it.
//
// The row is not deleted: a colleague may have placed orders and answered
// questions, and erase_user is the only door that removes a person. What the
// shop wants when somebody leaves is the history without the access.
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
	// Their sessions go with the access. Without this they keep the back office
	// for the rest of a session's life after being told they no longer have it.
	if err := s.q.EndStaffSessions(ctx, target); err != nil {
		return fmt.Errorf("end staff sessions: %w", err)
	}
	return nil
}

// RemoveFactor deletes somebody else's second factor, which is how a lost
// authenticator is recovered.
//
// Another admin only. Store.Remove has existed since 2FA shipped with a comment
// saying exactly that and no caller at all, so the documented recovery path was
// a paragraph: an admin who lost their phone was locked out of /admin for good
// and the only fix was SQL.
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
	// One admin left. Whether THIS is them is the question.
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
