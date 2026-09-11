package twofactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
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

// The staff forms record these and nothing else. A TOTP secret in after would
// live forever: audit_events is append-only and erase_user does not reach it.
const (
	actionGrantStaff        = "staff.grant"
	actionRevokeStaff       = "staff.revoke"
	actionRemoveStaffFactor = "staff.factor.remove"
)

// AddStaff gives somebody back-office access, with no password set. It is an
// UPSERT resolved by address, so submitting your own address is a
// self-promotion; the actor is compared for that reason.
//
// The bool is an outcome and not an error: the promotion succeeded, and true
// means the address already had an account which had never proved the mailbox,
// so its password was cleared and its sessions ended. The caller relays that.
func (s *Store) AddStaff(ctx context.Context, address, name, role, actorID string) (bool, error) {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	if !email.Valid(address) || !slices.Contains(pages.StaffRoles[:], pages.StaffRole(role)) {
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

	var cleared bool
	err = s.withStaffAudit(ctx, actor, actionGrantStaff, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
		credentialCleared, upsertErr := q.UpsertStaff(ctx, db.UpsertStaffParams{
			Email: address, FullName: name, Role: role,
		})
		if upsertErr != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](upsertErr); ok &&
				pgErr.ConstraintName == "users_keep_one_admin" {
				return uuid.UUID{}, nil, ErrLastAdmin
			}
			return uuid.UUID{}, nil, fmt.Errorf("add staff %s: %w", address, upsertErr)
		}
		cleared = credentialCleared
		target, lookupErr := q.UserByEmail(ctx, address)
		if lookupErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("read promoted staff %s: %w", address, lookupErr)
		}
		return target.ID, map[string]string{"email": target.Email, "role": target.Role}, nil
	})
	if err != nil {
		return false, err
	}
	return cleared, nil
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
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ErrInvalidStaff
	}

	return s.withStaffAudit(ctx, actor, actionRevokeStaff, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
		revoked, revokeErr := q.RevokeStaff(ctx, target)
		if revokeErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("revoke staff: %w", revokeErr)
		}
		if !revoked {
			// The database function asks the last-admin question under the shared
			// roster lock, so this is where it is answered. A Go pre-check would give
			// a nicer message and make the real guard almost unreachable.
			return uuid.UUID{}, nil, whyRevokeMatchedNothing(ctx, q, target)
		}
		row, lookupErr := q.UserByID(ctx, target)
		if lookupErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("read revoked staff: %w", lookupErr)
		}
		return target, map[string]string{"email": row.Email, "role": row.Role}, nil
	})
}

// RemoveFactor deletes somebody else's second factor, which is how a lost
// authenticator is recovered.
func (s *Store) RemoveFactor(ctx context.Context, userID, actorID string) error {
	if userID == actorID {
		return ErrSelf
	}
	target, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotEnrolled
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return ErrInvalidStaff
	}

	return s.withStaffAudit(ctx, actor, actionRemoveStaffFactor, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
		removed, removeErr := q.RemoveTOTPAndSessions(ctx, target)
		if removeErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("remove totp and end its sessions: %w", removeErr)
		}
		if !removed {
			return uuid.UUID{}, nil, ErrNotEnrolled
		}
		row, lookupErr := q.UserByID(ctx, target)
		if lookupErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("read recovered staff: %w", lookupErr)
		}
		return target, map[string]string{"email": row.Email}, nil
	})
}

// withStaffAudit runs a staff write and records who did it in one transaction:
// an audit row for a rolled-back grant is a lie, and a grant that commits
// without one is a gap /admin/audit cannot close.
func (s *Store) withStaffAudit(
	ctx context.Context,
	actor uuid.UUID,
	action string,
	work func(context.Context, *db.Queries) (uuid.UUID, any, error),
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", action, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	target, after, err := work(ctx, q)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("encode %s: %w", action, err)
	}
	requestID := web.RequestID(ctx)
	if _, err := q.RecordAuditEvent(ctx, db.RecordAuditEventParams{
		Actor:       actor,
		Action:      action,
		EntityTable: "users",
		EntityID:    uuid.NullUUID{UUID: target, Valid: true},
		After:       payload,
		RequestID:   pgtype.Text{String: requestID, Valid: requestID != ""},
	}); err != nil {
		return fmt.Errorf("record %s: %w", action, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", action, err)
	}
	return nil
}

// whyRevokeMatchedNothing turns a zero row count into the sentence the staff
// member needs: the target was not staff at all, or it was the last admin.
func whyRevokeMatchedNothing(ctx context.Context, q *db.Queries, target uuid.UUID) error {
	rows, err := q.StaffTOTPStatus(ctx)
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
