//go:build integration

package staff_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/web"
)

// TestAnotherAdminCanRecoverALostAuthenticator holds the recovery path: another
// admin removes the credential, and there are no printed backup codes.
func TestAnotherAdminCanRecoverALostAuthenticator(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	lost, _ := admintest.AdminUser(t, pool)
	helper, _ := admintest.AdminUser(t, pool)
	enrolFactor(t, lost)

	if !enrolled(t, lost) {
		t.Fatal("the fixture did not enrol anybody")
	}
	if err := s.RemoveFactor(asActor(ctx, helper), lost); err != nil {
		t.Fatalf("another admin could not remove the credential: %v", err)
	}
	if enrolled(t, lost) {
		t.Error("the credential survived")
	}

	// Enrolling again on the new phone is the point, not the removal itself.
	enrolFactor(t, lost)
	if !enrolled(t, lost) {
		t.Error("the account could not enrol again")
	}
}

// TestAnAdminCannotRemoveTheirOwnFactor is the guard that makes the recovery
// path safe: the session doing it is already step-up verified.
func TestAnAdminCannotRemoveTheirOwnFactor(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	self, _ := admintest.AdminUser(t, pool)
	enrolFactor(t, self)

	for _, submitted := range spellingsOf(self) {
		if err := s.RemoveFactor(asActor(ctx, self), submitted); !errors.Is(err, staff.ErrSelf) {
			t.Errorf("an admin removed their own factor as %q: %v", submitted, err)
		}
		if !enrolled(t, self) {
			t.Fatalf("the credential was removed anyway, as %q", submitted)
		}
	}
}

// TestTheLastAdminCannotBeRevoked. Removing the only admin leaves nobody who
// can add one back.
func TestTheLastAdminCannotBeRevoked(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	only, _ := admintest.AdminUser(t, pool)
	// This suite's other tests create admins, so leave exactly the intended one.
	// The database invariant correctly refuses a transition all the way to zero.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1`, only); err != nil {
		t.Fatalf("leave one admin: %v", err)
	}
	other, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, other); err != nil {
		t.Fatalf("demote the helper: %v", err)
	}

	if err := s.RevokeStaff(asActor(ctx, other), only); !errors.Is(err, staff.ErrLastAdmin) {
		t.Fatalf("the last admin was revocable: %v", err)
	}
	if roleOf(t, only) != "admin" {
		t.Errorf("the last admin is now %q", roleOf(t, only))
	}

	// The control: with a second admin, the first one can go.
	second, _ := admintest.AdminUser(t, pool)
	if err := s.RevokeStaff(asActor(ctx, second), only); err != nil {
		t.Errorf("with two admins the first could not be revoked: %v", err)
	}
}

// TestRevokingAccessEndsTheSessionsThatHadIt, for the rest of a session's life.
func TestRevokingAccessEndsTheSessionsThatHadIt(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	leaving, _ := admintest.AdminUser(t, pool)
	remaining, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256('leaving-token'::bytea), $1, now() + interval '1 hour')`,
		leaving); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.RevokeStaff(asActor(ctx, remaining), leaving); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var sessions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1`, leaving).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("%d sessions survive a revoked colleague", sessions)
	}
}

// TestANewColleagueHasNoPassword: they set their own through /forgot, the one
// path that proves they own the mailbox.
func TestANewColleagueHasNoPassword(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	actor, _ := admintest.AdminUser(t, pool)

	const address = "newcolleague@goen.invalid"
	if _, err := s.AddStaff(asActor(ctx, actor), address, "新同事", "staff"); err != nil {
		t.Fatalf("add: %v", err)
	}

	var role string
	var hasPassword bool
	if err := pool.QueryRow(ctx,
		`SELECT role, password_hash IS NOT NULL FROM users WHERE lower(email) = lower($1)`,
		address).Scan(&role, &hasPassword); err != nil {
		t.Fatalf("read the account: %v", err)
	}
	if role != "staff" {
		t.Errorf("the new colleague is %q, want staff", role)
	}
	if hasPassword {
		t.Error("a password was set for somebody else")
	}

	// An existing CUSTOMER is promoted rather than refused.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, role) VALUES ('shopper@goen.invalid', 'customer')`); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, err := s.AddStaff(asActor(ctx, actor), "shopper@goen.invalid", "", "admin"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	var promoted string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM users WHERE lower(email) = lower('shopper@goen.invalid')`).Scan(&promoted); err != nil {
		t.Fatalf("read the promoted account: %v", err)
	}
	if promoted != "admin" {
		t.Errorf("the existing customer is %q, want admin", promoted)
	}
}

// TestNobodyCanPromoteThemselvesThroughTheStaffForm. AddStaff is an upsert
// resolved by address, so POSTing your own email with role=admin is a
// self-promotion; the case fold is asserted because users_email_key folds too.
func TestNobodyCanPromoteThemselvesThroughTheStaffForm(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	actor, address := admintest.AdminUser(t, pool)
	// A SECOND admin, because the demotion below is refused outright when the
	// actor is the only one — users_last_admin is a database rule, not a Go
	// check. Which admins exist is shared state and the suite shuffles, so a
	// fixture relying on another test's leftover admin passes only in some
	// orderings.
	admintest.AdminUser(t, pool)
	// Demoted first, or "is still admin" of an admin cannot fail.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, actor); err != nil {
		t.Fatalf("demote the actor: %v", err)
	}

	for _, submitted := range []string{address, strings.ToUpper(address)} {
		t.Run(submitted, func(t *testing.T) {
			if _, err := s.AddStaff(asActor(ctx, actor), submitted, "我自己", "admin"); !errors.Is(err, staff.ErrSelf) {
				t.Errorf("AddStaff on the actor's own address = %v, want ErrSelf", err)
			}
		})
	}

	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM users WHERE id = $1`, actor).Scan(&role); err != nil {
		t.Fatalf("read the actor: %v", err)
	}
	if role == "admin" {
		t.Error("the actor promoted themselves to admin")
	}

	// The CONTROL: a store that refused everything would pass the above.
	other := "colleague-" + uuid.NewString() + "@goen.invalid"
	if _, err := s.AddStaff(asActor(ctx, actor), other, "同事", "staff"); err != nil {
		t.Errorf("adding a colleague was refused: %v", err)
	}
}

// TestPromotingAnUnprovedAccountTakesItsCredential closes a full takeover.
//
// goen does not verify an address at registration, so somebody who knows an
// address is about to be hired can register it FIRST with their own password,
// keep a live session, and wait: sessions read users.role live on every
// request, so a promotion that left the password and the sessions in place
// would hand the back office to whoever registered the address, and StaffOnly
// would then let that session enrol its own second factor.
//
// A new colleague gets NO password and proves the mailbox through /forgot, and
// an account that has never proved its address is in exactly that position,
// whoever created it.
func TestPromotingAnUnprovedAccountTakesItsCredential(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	actor, _ := admintest.AdminUser(t, pool)

	const address = "newhire@goen.invalid"
	// The attacker registers the address first, with their own password.
	var attacker string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, password_hash)
		VALUES ($1, 'customer', '$argon2id$v=19$m=65536,t=1,p=4$attacker')
		RETURNING id`, address).Scan(&attacker); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256('attacker-token'::bytea), $1, now() + interval '14 days')`,
		attacker); err != nil {
		t.Fatalf("attacker session: %v", err)
	}

	// The shop hires the person that address belongs to.
	cleared, err := s.AddStaff(asActor(ctx, actor), address, "新同事", "admin")
	if err != nil {
		t.Fatalf("AddStaff = %v, want nil — the promotion itself succeeds", err)
	}
	if !cleared {
		t.Fatal("AddStaff reported nothing cleared, so the admin is not told that " +
			"the account they promoted had never proved its address")
	}

	var role string
	var hasPassword bool
	var sessions int
	if err := pool.QueryRow(ctx, `
		SELECT u.role, u.password_hash IS NOT NULL,
		       (SELECT count(*) FROM sessions s WHERE s.user_id = u.id)
		FROM users u WHERE u.id = $1`, attacker).Scan(&role, &hasPassword, &sessions); err != nil {
		t.Fatalf("read the promoted account: %v", err)
	}
	if role != "admin" {
		t.Errorf("role = %q, want admin — the promotion itself must still happen", role)
	}
	if hasPassword {
		t.Error("the promoted account kept the password whoever registered it chose; " +
			"that password now opens the back office")
	}
	if sessions != 0 {
		t.Errorf("%d sessions predating the promotion survive it, and role is read "+
			"live per request", sessions)
	}
}

// TestPromotingAProvedAccountKeepsIt is the other half, and the reason the rule
// asks about the ADDRESS rather than about promotion: a verified address
// provably belongs to whoever reads that mailbox, which is the person being
// hired. Clearing their password would be friction bought with nothing, in the
// common case of a shop hiring somebody who already shops there.
func TestPromotingAProvedAccountKeepsIt(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	actor, _ := admintest.AdminUser(t, pool)

	const address = "provencustomer@goen.invalid"
	var customer string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, password_hash, email_verified_at)
		VALUES ($1, 'customer', '$argon2id$v=19$m=65536,t=1,p=4$theirs', now())
		RETURNING id`, address).Scan(&customer); err != nil {
		t.Fatalf("register: %v", err)
	}

	cleared, err := s.AddStaff(asActor(ctx, actor), address, "老顧客", "staff")
	if err != nil {
		t.Fatalf("AddStaff = %v, want nil — a proved account is promoted as it was", err)
	}
	if cleared {
		t.Error("a customer who had proved their address was reported as cleared")
	}
	var hasPassword bool
	if err := pool.QueryRow(ctx,
		`SELECT password_hash IS NOT NULL FROM users WHERE id = $1`, customer).Scan(&hasPassword); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !hasPassword {
		t.Error("a customer who had proved their address lost their password on being hired")
	}
}

// TestPromotionEndsTheSessionsOpenedBeforeIt holds the half the credential test
// cannot see: secure_promoted_account ends sessions ALWAYS, and not only when
// it cleared an unproved password.
//
// role is read live on every request, so a session opened before the promotion
// becomes a back-office session the moment the role moves: one the shop had not
// yet decided to trust with /admin, on whatever machine it was left open.
func TestPromotionEndsTheSessionsOpenedBeforeIt(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	actor, _ := admintest.AdminUser(t, pool)

	address := "sessioncustomer" + uuid.NewString()[:8] + "@goen.invalid"
	var customer string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, password_hash, email_verified_at)
		VALUES ($1, 'customer', '$argon2id$v=19$m=65536,t=1,p=4$theirs', now())
		RETURNING id`, address).Scan(&customer); err != nil {
		t.Fatalf("register: %v", err)
	}
	// PROVED, deliberately: the unproved path clears the password too, so the
	// two rules agree there and the fixture would test neither.
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256($1::bytea), $2, now() + interval '30 days')`,
		"before-promotion-"+customer, customer); err != nil {
		t.Fatalf("open a session: %v", err)
	}

	if _, err := s.AddStaff(asActor(ctx, actor), address, "老顧客", "staff"); err != nil {
		t.Fatalf("AddStaff: %v", err)
	}

	var live int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1`, customer).Scan(&live); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live != 0 {
		t.Errorf("%d session(s) opened before the promotion are still live, and "+
			"role is read live on every request — so each is now a back-office "+
			"session nobody decided to open", live)
	}
}

// TestRecoveringAFactorEndsTheSessionsItAdmitted holds the other door into the
// room RevokeStaff already guards. A session carries its own step-up stamp that
// SessionTOTPVerified trusts for the rest of StepUpWindow, so a session left
// open keeps the back office for up to twelve hours after the credential it was
// admitted on was removed — and can use StaffOnly to enrol a replacement of the
// attacker's own choosing, which is exactly the path this function exists to be.
func TestRecoveringAFactorEndsTheSessionsItAdmitted(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	victim, _ := admintest.AdminUser(t, pool)
	other, _ := admintest.AdminUser(t, pool)
	enrolFactor(t, victim)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, totp_verified_at)
		VALUES (sha256('stolen-token'::bytea), $1, now() + interval '14 days', now())`,
		victim); err != nil {
		t.Fatalf("create the stepped-up session: %v", err)
	}

	if err := s.RemoveFactor(asActor(ctx, other), victim); err != nil {
		t.Fatalf("recover: %v", err)
	}

	var sessions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1`, victim).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Errorf("%d sessions survive the removal of the factor they were admitted on; "+
			"each keeps the back office until its step-up stamp expires, and can enrol "+
			"a new factor from there", sessions)
	}
}

// TestRecoveringAFactorRollsBackWhenSessionRevocationFails proves the factor
// and the sessions are one security change. A failure after deleting only the
// factor would leave the exact stepped-up session recovery exists to end.
func TestRecoveringAFactorRollsBackWhenSessionRevocationFails(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	victim, _ := admintest.AdminUser(t, pool)
	other, _ := admintest.AdminUser(t, pool)
	enrolFactor(t, victim)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, totp_verified_at)
		VALUES (sha256($1::bytea), $2, now() + interval '14 days', now())`,
		"rollback-stolen-"+victim, victim); err != nil {
		t.Fatalf("create stepped-up session: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_factor_session_delete_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_factor_session_delete_" + suffix}.Sanitize()
	constraintName := "test_factor_session_delete_" + suffix
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.user_id = '%s'::uuid THEN
				RAISE EXCEPTION USING
					MESSAGE = 'forced session revocation failure',
					ERRCODE = 'check_violation',
					CONSTRAINT = '%s';
			END IF;
			RETURN OLD;
		END
		$body$;
		CREATE TRIGGER %s BEFORE DELETE ON sessions
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, victim, constraintName, triggerName, functionName)); err != nil {
		t.Fatalf("install session failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON sessions; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})

	err := s.RemoveFactor(asActor(ctx, other), victim)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != constraintName {
		t.Fatalf("RemoveFactor = %v, want forced session failure", err)
	}
	if !enrolled(t, victim) {
		t.Error("the credential was deleted even though its sessions could not be ended")
	}
	var sessions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id = $1`, victim).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions == 0 {
		t.Error("the forced failure did not leave the session in place, so the rollback test proved nothing")
	}
}

// TestTwoAdminsRevokingEachOtherLeaveOne holds the last-admin guard against a
// read-then-write: counting on the pool and writing separately lets two admins
// revoking each other both read two, both pass `admins > 1`, and both write.
// Zero admins has no way back — granting the role needs /admin/staff, which
// needs an admin.
func TestTwoAdminsRevokingEachOtherLeaveOne(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)

	a, _ := admintest.AdminUser(t, pool)
	b, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1 AND id <> $2`,
		a, b); err != nil {
		t.Fatalf("leave exactly these two admins: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id IN ($1, $2)`, a, b); err != nil {
		t.Fatalf("make both admins: %v", err)
	}

	// T1 holds its revoke open across T2's whole attempt: two goroutines behind
	// a start channel finish microseconds apart and never overlap.
	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := t1.Exec(ctx, `
		WITH admins AS (SELECT u.id AS admin_id FROM users u WHERE u.role = 'admin' FOR UPDATE)
		UPDATE users SET role = 'customer'
		WHERE users.id = $1 AND users.role IN ('staff','admin')
		  AND (users.role <> 'admin' OR (SELECT count(*) FROM admins) > 1)`, a); err != nil {
		t.Fatalf("T1 revoke: %v", err)
	}

	revoked := make(chan error, 1)
	go func() { revoked <- s.RevokeStaff(asActor(context.WithoutCancel(ctx), a), b) }()

	select {
	case err := <-revoked:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}

	if err := <-revoked; !errors.Is(err, staff.ErrLastAdmin) {
		t.Errorf("the second revoke returned %v, want ErrLastAdmin", err)
	}

	var admins int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if admins == 0 {
		t.Error("both revokes went through and the shop is locked out of its own " +
			"back office, with no path back")
	}
}

// TestErasureAndRevocationShareTheRosterGuard covers the cross-feature race:
// erasing A and revoking B’s admin access must not each observe the other
// as the remaining administrator. Both application roles enter through their
// real database doors and are held at the shared guard before either can write.
func TestErasureAndRevocationShareTheRosterGuard(t *testing.T) {
	ctx := t.Context()
	a, _ := admintest.AdminUser(t, pool)
	b, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE users SET role = 'customer'
		WHERE role = 'admin' AND id <> $1 AND id <> $2`, a, b); err != nil {
		t.Fatalf("leave exactly the race admins: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin roster blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT lock_admin_roster()`); err != nil {
		t.Fatalf("lock admin roster: %v", err)
	}

	suffix := uuid.NewString()[:8]
	eraseName, revokeName := "erase-revoke-erase-"+suffix, "erase-revoke-role-"+suffix
	eraser := account.NewStore(rolePool(t, eraseName, "store"))
	staffStore := staff.NewStore(rolePool(t, revokeName, "admin"))
	eraseDone, revokeDone := make(chan error, 1), make(chan error, 1)
	go func() { eraseDone <- eraser.Erase(context.WithoutCancel(ctx), a) }()
	go func() {
		revokeErr := staffStore.RevokeStaff(asActor(context.WithoutCancel(ctx), a), b)
		revokeDone <- revokeErr
	}()
	waitForLock(t, eraseName, eraseDone)
	waitForLock(t, revokeName, revokeDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release roster blocker: %v", err)
	}
	eraseErr := operationResult(t, eraseDone)
	revokeErr := operationResult(t, revokeDone)
	eraseRefused := func(err error) bool {
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		return ok && pgErr.ConstraintName == "erase_user_keeps_one_admin"
	}
	eraseWon := eraseErr == nil && errors.Is(revokeErr, staff.ErrLastAdmin)
	revokeWon := revokeErr == nil && eraseRefused(eraseErr)
	if !eraseWon && !revokeWon {
		t.Fatalf("erase/revoke = %v / %v, want one success and one last-admin refusal",
			eraseErr, revokeErr)
	}

	var admins int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if admins != 1 {
		t.Errorf("erase/revoke left %d admins, want 1", admins)
	}
}

// TestStaffAuthorizationChangesLeaveATrail is the lock for #122: each staff
// form that actually changes authorization or a factor writes one audit row
// naming the actor and the target. A drop of the record call is a silent
// back-office grant.
func TestStaffAuthorizationChangesLeaveATrail(t *testing.T) {
	ctx := web.WithRequestID(t.Context(), "req-staff-audit")
	s := staff.NewStore(pool)
	actor, _ := admintest.AdminUser(t, pool)

	address := "audited-" + uuid.NewString()[:8] + "@goen.invalid"
	beforeGrant := staffAuditCount(t, "staff.grant")
	if _, err := s.AddStaff(asActor(ctx, actor), address, "稽核同事", "staff"); err != nil {
		t.Fatalf("AddStaff: %v", err)
	}
	if after := staffAuditCount(t, "staff.grant"); after != beforeGrant+1 {
		t.Fatalf("AddStaff left %d staff.grant rows, want %d", after, beforeGrant+1)
	}
	var target string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE lower(email) = lower($1)`, address).Scan(&target); err != nil {
		t.Fatalf("read promoted id: %v", err)
	}
	gotActor, gotEmail, gotRole := readStaffAudit(t, "staff.grant", target)
	if gotActor != actor || gotEmail != address || gotRole != "staff" {
		t.Errorf("staff.grant = actor %s email %q role %q; want %s/%q/staff",
			gotActor, gotEmail, gotRole, actor, address)
	}

	beforeRevoke := staffAuditCount(t, "staff.revoke")
	if err := s.RevokeStaff(asActor(ctx, actor), target); err != nil {
		t.Fatalf("RevokeStaff: %v", err)
	}
	if after := staffAuditCount(t, "staff.revoke"); after != beforeRevoke+1 {
		t.Fatalf("RevokeStaff left %d staff.revoke rows, want %d", after, beforeRevoke+1)
	}
	gotActor, gotEmail, gotRole = readStaffAudit(t, "staff.revoke", target)
	if gotActor != actor || gotEmail != address || gotRole != "customer" {
		t.Errorf("staff.revoke = actor %s email %q role %q; want %s/%q/customer",
			gotActor, gotEmail, gotRole, actor, address)
	}

	lost, lostEmail := admintest.AdminUser(t, pool)
	enrolFactor(t, lost)
	beforeRemove := staffAuditCount(t, "staff.factor.remove")
	if err := s.RemoveFactor(asActor(ctx, actor), lost); err != nil {
		t.Fatalf("RemoveFactor: %v", err)
	}
	if after := staffAuditCount(t, "staff.factor.remove"); after != beforeRemove+1 {
		t.Fatalf("RemoveFactor left %d staff.factor.remove rows, want %d", after, beforeRemove+1)
	}
	gotActor, gotEmail, _ = readStaffAudit(t, "staff.factor.remove", lost)
	if gotActor != actor || gotEmail != lostEmail {
		t.Errorf("staff.factor.remove = actor %s email %q; want %s/%q",
			gotActor, gotEmail, actor, lostEmail)
	}
	var secretInTrail int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'staff.factor.remove' AND entity_id = $1
		  AND (after ? 'secret' OR after::text ILIKE '%secret%')`, lost).Scan(&secretInTrail); err != nil {
		t.Fatalf("look for a secret in the trail: %v", err)
	}
	if secretInTrail != 0 {
		t.Error("factor-remove after retained TOTP material")
	}
}

// TestRefusedStaffChangesLeaveNoTrail: a no-op must not claim a completed
// authorization change on /admin/audit.
func TestRefusedStaffChangesLeaveNoTrail(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	self, selfEmail := admintest.AdminUser(t, pool)
	helper, _ := admintest.AdminUser(t, pool)

	beforeGrant := staffAuditCount(t, "staff.grant")
	if _, err := s.AddStaff(asActor(ctx, self), selfEmail, "我自己", "admin"); !errors.Is(err, staff.ErrSelf) {
		t.Fatalf("self AddStaff = %v, want ErrSelf", err)
	}
	if _, err := s.AddStaff(asActor(ctx, self), "not-an-email", "無效", "staff"); !errors.Is(err, staff.ErrInvalidStaff) {
		t.Fatalf("invalid AddStaff = %v, want ErrInvalidStaff", err)
	}
	if after := staffAuditCount(t, "staff.grant"); after != beforeGrant {
		t.Errorf("refused AddStaff left %d staff.grant rows, want %d", after, beforeGrant)
	}

	beforeRevoke := staffAuditCount(t, "staff.revoke")
	for _, submitted := range spellingsOf(self) {
		if err := s.RevokeStaff(asActor(ctx, self), submitted); !errors.Is(err, staff.ErrSelf) {
			t.Fatalf("self RevokeStaff as %q = %v, want ErrSelf", submitted, err)
		}
	}
	if after := staffAuditCount(t, "staff.revoke"); after != beforeRevoke {
		t.Errorf("self RevokeStaff left %d staff.revoke rows, want %d", after, beforeRevoke)
	}

	enrolFactor(t, self)
	beforeRemove := staffAuditCount(t, "staff.factor.remove")
	for _, submitted := range spellingsOf(self) {
		if err := s.RemoveFactor(asActor(ctx, self), submitted); !errors.Is(err, staff.ErrSelf) {
			t.Fatalf("self RemoveFactor as %q = %v, want ErrSelf", submitted, err)
		}
	}
	missing := uuid.NewString()
	if err := s.RemoveFactor(asActor(ctx, helper), missing); !errors.Is(err, staff.ErrNotEnrolled) {
		t.Fatalf("RemoveFactor on nobody = %v, want ErrNotEnrolled", err)
	}
	if after := staffAuditCount(t, "staff.factor.remove"); after != beforeRemove {
		t.Errorf("refused RemoveFactor left %d staff.factor.remove rows, want %d", after, beforeRemove)
	}

	only, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1`, only); err != nil {
		t.Fatalf("leave one admin: %v", err)
	}
	other, _ := admintest.AdminUser(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, other); err != nil {
		t.Fatalf("demote the helper: %v", err)
	}
	beforeLast := staffAuditCount(t, "staff.revoke")
	if err := s.RevokeStaff(asActor(ctx, other), only); !errors.Is(err, staff.ErrLastAdmin) {
		t.Fatalf("last-admin RevokeStaff = %v, want ErrLastAdmin", err)
	}
	if after := staffAuditCount(t, "staff.revoke"); after != beforeLast {
		t.Errorf("last-admin revoke left %d staff.revoke rows, want %d", after, beforeLast)
	}
	if roleOf(t, only) != "admin" {
		t.Errorf("the last admin is now %q", roleOf(t, only))
	}
}

// TestStaffWriteRollsBackWhenAuditCannotRecord: a grant that cannot be
// attributed must not land. The missing actor fails the audit FK after the
// upsert, so seeing no user proves the two statements shared a transaction.
func TestStaffWriteRollsBackWhenAuditCannotRecord(t *testing.T) {
	ctx := t.Context()
	s := staff.NewStore(pool)
	missing := uuid.NewString()
	address := "unattributed-" + uuid.NewString()[:8] + "@goen.invalid"

	_, err := s.AddStaff(asActor(ctx, missing), address, "不得落地", "staff")
	if err == nil {
		t.Fatal("AddStaff with an unrecordable actor succeeded")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "audit_events_actor_user_id_fkey" {
		t.Fatalf("AddStaff audit failure = %v, want audit actor FK", err)
	}

	var users, audits int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, address).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'staff.grant' AND after->>'email' = $1`, address).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if users != 0 || audits != 0 {
		t.Errorf("unattributed grant left %d user(s) and %d audit row(s), want 0/0", users, audits)
	}
}
