// Package staff is the back office's roster: who is staff or admin, granting and
// revoking that access, and removing a colleague's lost second factor.
package staff

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/user"
)

var (
	ErrLastAdmin = errors.New("staff: that would leave no admin")
	// ErrSelf is an admin acting on their own account. The session doing it is
	// already step-up verified, so self-service would turn a stolen session into
	// permanent access.
	ErrSelf         = errors.New("staff: an admin cannot do that to their own account")
	ErrInvalidStaff = errors.New("staff: that is not a usable staff account")
	ErrAlreadyStaff = errors.New("staff: that account is already staff")
	ErrNotEnrolled  = errors.New("staff: that account has no second factor to remove")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("staff: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// AddStaff creates a colleague or promotes a customer. An existing colleague
// must use the separate authorization operations so an add cannot change their
// role or end their sessions.
//
// The bool is an outcome and not an error: the promotion succeeded, and true
// means the address already had an account which had never proved the mailbox,
// so its password was cleared and its sessions ended. The caller relays that.
func (s *Store) AddStaff(ctx context.Context, address, name, role string) (bool, error) {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	if !email.Valid(address) || !slices.Contains(user.StaffRoles[:], user.Role(role)) {
		return false, ErrInvalidStaff
	}
	actor, ok := audit.Actor(ctx)
	if !ok {
		return false, audit.ErrNoActor
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
	err = s.write(ctx, audit.ActionGrantStaff, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
		credentialCleared, upsertErr := q.UpsertStaff(ctx, db.UpsertStaffParams{
			Email: address, FullName: name, Role: role,
		})
		if upsertErr != nil {
			switch {
			case pgerr.IsConstraint(upsertErr, "users_keep_one_admin"):
				return uuid.UUID{}, nil, ErrLastAdmin
			case pgerr.IsConstraint(upsertErr, "users_staff_already_exists"):
				return uuid.UUID{}, nil, ErrAlreadyStaff
			}
			return uuid.UUID{}, nil, fmt.Errorf("add staff %s: %w", address, upsertErr)
		}
		cleared = credentialCleared
		target, lookupErr := q.UserByEmail(ctx, address)
		if lookupErr != nil {
			return uuid.UUID{}, nil, fmt.Errorf("read promoted staff %s: %w", address, lookupErr)
		}
		if enqueueErr := enqueueStaffInvitation(ctx, q, target.ID); enqueueErr != nil {
			return uuid.UUID{}, nil, enqueueErr
		}
		return target.ID, map[string]string{"email": target.Email, "role": target.Role}, nil
	})
	if err != nil {
		return false, err
	}
	return cleared, nil
}

func (s *Store) RevokeStaff(ctx context.Context, userID string) error {
	target, err := uuid.Parse(userID)
	if err != nil {
		return ErrInvalidStaff
	}
	actor, ok := audit.Actor(ctx)
	if !ok {
		return audit.ErrNoActor
	}
	// Compared parsed: uuid.Parse also reads upper case, {braces} and urn:uuid:,
	// so comparing the submitted text lets another spelling of oneself through.
	if target == actor {
		return ErrSelf
	}

	return s.write(ctx, audit.ActionRevokeStaff, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
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
func (s *Store) RemoveFactor(ctx context.Context, userID string) error {
	target, err := uuid.Parse(userID)
	if err != nil {
		return ErrNotEnrolled
	}
	actor, ok := audit.Actor(ctx)
	if !ok {
		return audit.ErrNoActor
	}
	// Compared parsed, for the reason RevokeStaff gives.
	if target == actor {
		return ErrSelf
	}

	return s.write(ctx, audit.ActionRemoveStaffFactor, func(ctx context.Context, q *db.Queries) (uuid.UUID, any, error) {
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

// write runs a staff write and records who did it in one transaction: an audit
// row for a rolled-back grant is a lie, and a grant that commits without one is
// a gap /admin/audit cannot close. work names the user it acted on, and what to
// record about them; a TOTP secret there would live forever, because
// audit_events is append-only and erase_user does not reach it.
func (s *Store) write(
	ctx context.Context,
	action audit.Action,
	work func(context.Context, *db.Queries) (target uuid.UUID, after any, err error),
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
	if err := audit.In(ctx, q, audit.Event{
		Action: action, Table: "users", ID: audit.EntityID(target), After: after,
	}); err != nil {
		return err
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
		if rows[i].ID == target && user.Role(rows[i].Role) == user.RoleAdmin {
			return ErrLastAdmin
		}
	}
	return ErrInvalidStaff
}

func (s *Store) Staff(ctx context.Context) (admin.StaffView, error) {
	rows, err := s.q.StaffTOTPStatus(ctx)
	if err != nil {
		return admin.StaffView{}, fmt.Errorf("read staff 2FA status: %w", err)
	}
	view := admin.StaffView{Rows: make([]admin.StaffRow, 0, len(rows))}
	// Cloned: a slice of the package-level array would let a caller write through it.
	view.Roles = slices.Clone(user.StaffRoles[:])
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.StaffRow{
			ID: r.ID.String(), Email: r.Email, Name: r.FullName,
			Role: user.Role(r.Role), Enrolled: r.Enrolled,
		})
	}
	return view, nil
}
