//go:build integration

package twofactor_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/koopa0/goen/internal/twofactor"
)

var pool *pgxpool.Pool

const testKey = "a-key-only-this-test-uses"

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("goen"),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		panic(err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(err)
	}
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		panic(err)
	}
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		panic(err)
	}
	code := m.Run()
	pool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// staff creates an admin and returns their id and email.
func staff(t *testing.T) (userID, email string) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('totp-' || gen_random_uuid() || '@goen.invalid', 'admin', '測試')
		RETURNING id, email`).Scan(&id, &email); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	return id.String(), email
}

// enrol takes a user all the way to a confirmed credential.
func enrol(t *testing.T, s *twofactor.Store, userID, email string) []byte {
	t.Helper()
	secret, _, err := s.Begin(t.Context(), userID, email)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	code := twofactor.Code(secret, twofactor.StepAt(time.Now()))
	if err := s.Confirm(t.Context(), userID, code); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return secret
}

// TestACodeIsAcceptedExactlyOnceEvenConcurrently proves the replay guard is in
// the statement. A sequential test passes with the guard in Go.
func TestACodeIsAcceptedExactlyOnceEvenConcurrently(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	secret := enrol(t, s, userID, email)

	_ = secret

	// The STATEMENT is driven directly: calling Store.Verify from N goroutines
	// staggers enough that later reads see earlier writes, so that version
	// passed with the guard removed from the SQL.
	var current int64
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(last_step, 0) FROM staff_totp_credentials WHERE user_id = $1`,
		uuid.MustParse(userID)).Scan(&current); err != nil {
		t.Fatalf("read step: %v", err)
	}
	step := current + 1

	const racers = 8
	start := make(chan struct{})
	affected := make([]int64, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			tag, err := pool.Exec(ctx, `
				UPDATE staff_totp_credentials
				SET last_step = $2
				WHERE user_id = $1
				  AND confirmed_at IS NOT NULL
				  AND (last_step IS NULL OR last_step < $2)`,
				uuid.MustParse(userID), step)
			if err == nil {
				affected[i] = tag.RowsAffected()
			}
		})
	}
	close(start)
	wg.Wait()

	var wins int64
	for _, n := range affected {
		wins += n
	}
	if wins != 1 {
		t.Errorf("%d of %d concurrent claims of step %d succeeded, want 1 — the "+
			"guard is not in the statement, so a code can be replayed by two "+
			"simultaneous requests", wins, racers, step)
	}

	if err := s.Verify(ctx, userID, twofactor.Code(secret, step)); err == nil {
		t.Error("the code for a spent step was accepted")
	}
}

// TestRestartingEnrolmentInvalidatesTheOldSecret proves a replaced secret stops
// working immediately.
func TestRestartingEnrolmentInvalidatesTheOldSecret(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	old := enrol(t, s, userID, email)

	// Through the RECOVERY path, the only way a proved factor is replaced.
	if err := s.Remove(ctx, userID); err != nil {
		t.Fatalf("remove the old factor: %v", err)
	}

	fresh, _, beginErr := s.Begin(ctx, userID, email)
	if beginErr != nil {
		t.Fatalf("re-begin: %v", beginErr)
	}

	step := twofactor.StepAt(time.Now()) + 1
	if err := s.Confirm(ctx, userID, twofactor.Code(old, step)); err == nil {
		t.Error("a code from the replaced secret was accepted")
	}
	// Re-enrolment must also UNCONFIRM: otherwise somebody who started one and
	// walked away has working 2FA against a secret in no authenticator.
	enrolled, enrolErr := s.Enrolled(ctx, userID)
	if enrolErr != nil {
		t.Fatalf("enrolled: %v", enrolErr)
	}
	if enrolled {
		t.Error("a restarted enrolment left the credential confirmed against a " +
			"secret nobody has proved")
	}

	if err := s.Confirm(ctx, userID, twofactor.Code(fresh, step)); err != nil {
		t.Errorf("a code from the new secret was refused: %v", err)
	}
	if enrolled, enrolErr = s.Enrolled(ctx, userID); enrolErr != nil || !enrolled {
		t.Errorf("confirming the new secret did not enrol (err=%v)", enrolErr)
	}
}

// TestAConfirmedFactorCannotBeReplacedByItsOwnHolder proves a stolen password
// alone cannot swap the second factor for the attacker's own device.
func TestAConfirmedFactorCannotBeReplacedByItsOwnHolder(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	original := enrol(t, s, userID, email)

	if _, _, err := s.Begin(ctx, userID, email); !errors.Is(err, twofactor.ErrEnrolled) {
		t.Errorf("Begin over a confirmed credential = %v, want ErrEnrolled", err)
	}

	// A refusal that also broke the real holder's authenticator would be its
	// own lockout.
	if enrolled, err := s.Enrolled(ctx, userID); err != nil || !enrolled {
		t.Errorf("the confirmed credential was disturbed by the refused enrolment (err=%v)", err)
	}
	if err := s.Verify(ctx, userID, twofactor.Code(original, twofactor.StepAt(time.Now())+1)); err != nil {
		t.Errorf("the original secret stopped working after a refused re-enrolment: %v", err)
	}

	// An UNCONFIRMED enrolment is still restartable: nothing has been proved.
	other, otherEmail := staff(t)
	if _, _, err := s.Begin(ctx, other, otherEmail); err != nil {
		t.Fatalf("first enrolment: %v", err)
	}
	if _, _, err := s.Begin(ctx, other, otherEmail); err != nil {
		t.Errorf("restarting an unconfirmed enrolment was refused: %v", err)
	}
}

// TestAnUnconfirmedCredentialCannotVerify proves an unproved secret counts for
// nothing.
func TestAnUnconfirmedCredentialCannotVerify(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)

	secret, _, err := s.Begin(ctx, userID, email)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	code := twofactor.Code(secret, twofactor.StepAt(time.Now()))

	if err := s.Verify(ctx, userID, code); !errors.Is(err, twofactor.ErrBadCode) &&
		!errors.Is(err, twofactor.ErrNotEnrolled) {
		t.Errorf("an unconfirmed credential verified with %v", err)
	}
	enrolled, enrolErr := s.Enrolled(ctx, userID)
	if enrolErr != nil {
		t.Fatalf("enrolled: %v", enrolErr)
	}
	if enrolled {
		t.Error("an unconfirmed credential reported itself enrolled")
	}
}

// TestAStoredSecretIsNotReadableFromTheDatabase proves a dump alone does not
// defeat the factor.
func TestAStoredSecretIsNotReadableFromTheDatabase(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	secret := enrol(t, s, userID, email)

	var stored []byte
	if err := pool.QueryRow(ctx,
		`SELECT secret_encrypted FROM staff_totp_credentials WHERE user_id = $1`,
		uuid.MustParse(userID)).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(stored) == len(secret) {
		t.Error("the stored value is the same length as the plaintext; it is " +
			"probably not sealed")
	}
	if bytes.Contains(stored, secret) {
		t.Fatal("the plaintext secret appears in the stored bytes")
	}

	other := twofactor.NewStore(pool, "a-different-key")
	if err := other.Verify(ctx, userID, twofactor.Code(secret, twofactor.StepAt(time.Now())+1)); err == nil {
		t.Error("a credential verified under the wrong encryption key")
	}
}

// TestSessionVerificationExpires proves a proof does not last forever.
func TestSessionVerificationExpires(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, _ := staff(t)

	const token = "a-session-token-for-this-test"
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256($1::bytea), $2, now() + interval '1 day')`,
		[]byte(token), uuid.MustParse(userID)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	verified, err := s.SessionVerified(ctx, token)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if verified {
		t.Fatal("a session that never verified reported itself verified")
	}

	if markErr := s.MarkVerified(ctx, token); markErr != nil {
		t.Fatalf("mark: %v", markErr)
	}
	verified, err = s.SessionVerified(ctx, token)
	if err != nil || !verified {
		t.Fatalf("a just-verified session reads as unverified (err=%v)", err)
	}

	// Age it past the window.
	if _, ageErr := pool.Exec(ctx, `
		UPDATE sessions SET totp_verified_at = created_at
		WHERE token_hash = sha256($1::bytea)`, []byte(token)); ageErr != nil {
		t.Fatalf("age: %v", ageErr)
	}
	if _, ageErr := pool.Exec(ctx, `
		UPDATE sessions SET created_at = now() - interval '13 hours',
		    totp_verified_at = now() - interval '13 hours'
		WHERE token_hash = sha256($1::bytea)`, []byte(token)); ageErr != nil {
		t.Fatalf("age: %v", ageErr)
	}
	if verified, err = s.SessionVerified(ctx, token); err != nil {
		t.Fatalf("read: %v", err)
	}
	if verified {
		t.Errorf("a verification %v old still counts; the window is %v",
			13*time.Hour, twofactor.StepUpWindow)
	}

	// An unknown token is a signed-out request, not a failure.
	verified, err = s.SessionVerified(ctx, "no-such-token")
	if err != nil || verified {
		t.Errorf("an unknown token gave verified=%v err=%v", verified, err)
	}
}

// TestNoKeyMeansNoEnrolment proves an unconfigured deployment writes nothing.
func TestNoKeyMeansNoEnrolment(t *testing.T) {
	s := twofactor.NewStore(pool, "")
	userID, email := staff(t)

	if s.Enabled() {
		t.Fatal("a store with no key reported itself enabled")
	}
	if _, _, err := s.Begin(t.Context(), userID, email); !errors.Is(err, twofactor.ErrDisabled) {
		t.Errorf("Begin gave %v, want ErrDisabled", err)
	}

	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM staff_totp_credentials WHERE user_id = $1`,
		uuid.MustParse(userID)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("a credential was written with no encryption key configured")
	}
}

// TestAnotherAdminCanRecoverALostAuthenticator holds the recovery path: another
// admin removes the credential, and there are no printed backup codes.
func TestAnotherAdminCanRecoverALostAuthenticator(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	lost, lostEmail := staff(t)
	helper, _ := staff(t)
	enrol(t, s, lost, lostEmail)

	if !enrolled(t, lost) {
		t.Fatal("the fixture did not enrol anybody")
	}
	if err := s.RemoveFactor(ctx, lost, helper); err != nil {
		t.Fatalf("another admin could not remove the credential: %v", err)
	}
	if enrolled(t, lost) {
		t.Error("the credential survived")
	}

	// Enrolling again on the new phone is the point, not the removal itself.
	enrol(t, s, lost, lostEmail)
	if !enrolled(t, lost) {
		t.Error("the account could not enrol again")
	}
}

// TestAnAdminCannotRemoveTheirOwnFactor is the guard that makes the recovery
// path safe: the session doing it is already step-up verified.
func TestAnAdminCannotRemoveTheirOwnFactor(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	self, selfEmail := staff(t)
	enrol(t, s, self, selfEmail)

	if err := s.RemoveFactor(ctx, self, self); !errors.Is(err, twofactor.ErrSelf) {
		t.Errorf("an admin removed their own factor: %v", err)
	}
	if !enrolled(t, self) {
		t.Error("the credential was removed anyway")
	}
}

// TestTheLastAdminCannotBeRevoked. Removing the only admin leaves nobody who
// can add one back.
func TestTheLastAdminCannotBeRevoked(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	// This suite's other tests create admins, so the state is made not assumed.
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'customer' WHERE role = 'admin'`); err != nil {
		t.Fatalf("clear admins: %v", err)
	}
	only, _ := staff(t)
	other, _ := staff(t)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, other); err != nil {
		t.Fatalf("demote the helper: %v", err)
	}

	if err := s.RevokeStaff(ctx, only, other); !errors.Is(err, twofactor.ErrLastAdmin) {
		t.Fatalf("the last admin was revocable: %v", err)
	}
	if roleOf(t, only) != "admin" {
		t.Errorf("the last admin is now %q", roleOf(t, only))
	}

	// The control: with a second admin, the first one can go.
	second, _ := staff(t)
	if err := s.RevokeStaff(ctx, only, second); err != nil {
		t.Errorf("with two admins the first could not be revoked: %v", err)
	}
}

// TestRevokingAccessEndsTheSessionsThatHadIt, for the rest of a session's life.
func TestRevokingAccessEndsTheSessionsThatHadIt(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	leaving, _ := staff(t)
	remaining, _ := staff(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256('leaving-token'::bytea), $1, now() + interval '1 hour')`,
		leaving); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.RevokeStaff(ctx, leaving, remaining); err != nil {
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
	s := twofactor.NewStore(pool, testKey)
	actor, _ := staff(t)

	const address = "newcolleague@goen.invalid"
	if _, err := s.AddStaff(ctx, address, "新同事", "staff", actor); err != nil {
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
	if _, err := s.AddStaff(ctx, "shopper@goen.invalid", "", "admin", actor); err != nil {
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
	s := twofactor.NewStore(pool, testKey)
	actor, address := staff(t)
	// Demoted first, or "is still admin" of an admin cannot fail.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, actor); err != nil {
		t.Fatalf("demote the actor: %v", err)
	}

	for _, submitted := range []string{address, strings.ToUpper(address)} {
		t.Run(submitted, func(t *testing.T) {
			if _, err := s.AddStaff(ctx, submitted, "我自己", "admin", actor); !errors.Is(err, twofactor.ErrSelf) {
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
	if _, err := s.AddStaff(ctx, other, "同事", "staff", actor); err != nil {
		t.Errorf("adding a colleague was refused: %v", err)
	}
}

func enrolled(t *testing.T, userID string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM staff_totp_credentials WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	return n > 0
}

func roleOf(t *testing.T, userID string) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(t.Context(),
		`SELECT role FROM users WHERE id = $1`, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

// TestPromotingAnUnprovedAccountTakesItsCredential closes a full takeover.
//
// goen does not verify an address at registration, which is recorded and
// deliberate. So somebody who knows an address is about to be hired can
// register it FIRST with their own password, keep a live session, and wait.
// UpsertStaff resolved ON CONFLICT and set only the role — leaving that
// password and those sessions exactly where they were — while sessions read
// users.role live on every request. The promotion handed the back office to
// whoever had registered the address, and StaffOnly then let the same session
// enrol its own second factor.
//
// The rule restored here is the one the statement beside it already stated: a
// new colleague gets NO password and proves the mailbox through /forgot. An
// account that has never proved its address is in exactly that position,
// whoever created it.
func TestPromotingAnUnprovedAccountTakesItsCredential(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	actor, _ := staff(t)

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
	cleared, err := s.AddStaff(ctx, address, "新同事", "admin", actor)
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
// asks about the ADDRESS rather than about promotion.
//
// A verified address provably belongs to whoever reads that mailbox — which is
// the person being hired. Clearing their password would be friction bought with
// nothing, and would make the common case (a shop hiring somebody who already
// shops there, which is the recorded reason promotion exists at all) worse than
// before.
func TestPromotingAProvedAccountKeepsIt(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	actor, _ := staff(t)

	const address = "provencustomer@goen.invalid"
	var customer string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, password_hash, email_verified_at)
		VALUES ($1, 'customer', '$argon2id$v=19$m=65536,t=1,p=4$theirs', now())
		RETURNING id`, address).Scan(&customer); err != nil {
		t.Fatalf("register: %v", err)
	}

	cleared, err := s.AddStaff(ctx, address, "老顧客", "staff", actor)
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

// TestRecoveringAFactorEndsTheSessionsItAdmitted holds the other door into the
// room RevokeStaff already guards.
//
// Removing a second factor is the moment it stops being proof. But a session
// carries its own step-up stamp that SessionTOTPVerified trusts for the rest of
// StepUpWindow, so a stolen session kept the back office for up to twelve hours
// after the credential it was admitted on was taken away — and could use
// StaffOnly to enrol a replacement of the attacker's own choosing, which is
// exactly the recovery path this function exists to be.
func TestRecoveringAFactorEndsTheSessionsItAdmitted(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	victim, victimEmail := staff(t)
	other, _ := staff(t)
	enrol(t, s, victim, victimEmail)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at, totp_verified_at)
		VALUES (sha256('stolen-token'::bytea), $1, now() + interval '14 days', now())`,
		victim); err != nil {
		t.Fatalf("create the stepped-up session: %v", err)
	}

	if err := s.RemoveFactor(ctx, victim, other); err != nil {
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

// TestTwoAdminsRevokingEachOtherLeaveOne holds a guard that used to be a
// read-then-write.
//
// CountAdmins ran on the pool and RevokeStaff wrote separately, so two admins
// revoking each other at once both read two, both passed `admins > 1`, and both
// wrote. Reproduced against a scratch database: zero admins left, and no way
// back — granting the role needs /admin/staff, which needs an admin. That is
// the state this page was built to make impossible.
func TestTwoAdminsRevokingEachOtherLeaveOne(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	a, _ := staff(t)
	b, _ := staff(t)
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
	go func() { revoked <- s.RevokeStaff(context.WithoutCancel(ctx), b, a) }()

	select {
	case err := <-revoked:
		t.Fatalf("T2 finished before T1 committed (%v); it never met the lock", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("commit T1: %v", err)
	}

	if err := <-revoked; !errors.Is(err, twofactor.ErrLastAdmin) {
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
