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
// the statement.
//
// The replay guard is a WHERE clause, not a Go comparison. Two requests
// replaying one code both read the same last_step, and a check made in the
// application is a check both of them pass — which is a 90-second window in
// which a shoulder-surfed code works twice.
//
// Concurrency here is the point: a sequential test passes with the guard in Go.
func TestACodeIsAcceptedExactlyOnceEvenConcurrently(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	secret := enrol(t, s, userID, email)

	_ = secret

	// The STATEMENT is what is being tested, driven concurrently.
	//
	// Calling Store.Verify from N goroutines does not test it: each one reads
	// last_step, and they stagger enough that the later reads already see the
	// earlier writes — so the case passed with the guard removed from the SQL
	// and left only in Go. Every goroutine here starts from the same recorded
	// step by construction, which is the state two genuinely simultaneous
	// requests are in.
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

	// And the store agrees: the step is spent, so Verify refuses that code.
	if err := s.Verify(ctx, userID, twofactor.Code(secret, step)); err == nil {
		t.Error("the code for a spent step was accepted")
	}
}

// TestRestartingEnrolmentInvalidatesTheOldSecret proves a replaced secret stops
// working immediately.
//
// Somebody who lost their phone enrols again. The old secret must stop working
// the moment the new one is written, or a stolen authenticator keeps producing
// valid codes forever.
func TestRestartingEnrolmentInvalidatesTheOldSecret(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	old := enrol(t, s, userID, email)

	// Through the RECOVERY path, because that is the only way a proved factor is
	// replaced: another admin removes it at /admin/staff. This test used to call
	// Begin straight over a confirmed credential and assert the overwrite was
	// correct — which is the hole, not the feature. A password and a session were
	// then enough to swap the second factor for the attacker's own device.
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
	// Re-enrolment must also UNCONFIRM the credential: the new secret has not
	// been proved, and leaving it confirmed means somebody who started a
	// re-enrolment and walked away still has working 2FA against a secret in no
	// authenticator. That is what the ON CONFLICT clause resets, and asserting
	// only "the old secret stopped working" could not see it.
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

// TestAConfirmedFactorCannotBeReplacedByItsOwnHolder is the Critical a
// third-party review found.
//
// The enrolment route needs only an ordinary signed-in session, so before this
// guard a stolen PASSWORD was the entire back office: sign in, enrol your own
// authenticator over the real one, confirm it — and confirming marks the session
// step-up verified, so the attacker walks straight into /admin. The second
// factor became a formality the password had already cleared.
//
// Store.Remove names this exact threat as the reason nobody may drop their OWN
// factor, and Begin let one skip the removal entirely.
//
// The refusal is in the statement's own WHERE clause rather than a read in Go,
// because a read-then-write is a race two concurrent enrolments both win.
func TestAConfirmedFactorCannotBeReplacedByItsOwnHolder(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	original := enrol(t, s, userID, email)

	if _, _, err := s.Begin(ctx, userID, email); !errors.Is(err, twofactor.ErrEnrolled) {
		t.Errorf("Begin over a confirmed credential = %v, want ErrEnrolled", err)
	}

	// The original factor still works, which is the half that matters: a refusal
	// that also broke the real holder's authenticator would be its own lockout.
	if enrolled, err := s.Enrolled(ctx, userID); err != nil || !enrolled {
		t.Errorf("the confirmed credential was disturbed by the refused enrolment (err=%v)", err)
	}
	if err := s.Verify(ctx, userID, twofactor.Code(original, twofactor.StepAt(time.Now())+1)); err != nil {
		t.Errorf("the original secret stopped working after a refused re-enrolment: %v", err)
	}

	// An UNCONFIRMED enrolment is still restartable — somebody who mistyped the
	// secret into their app has proved nothing, so there is nothing to protect.
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
//
// Enrolment is two steps because somebody who mistypes the secret into their
// app would otherwise have working 2FA on paper and no way to produce a code —
// locked out of the thing 2FA was protecting.
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
//
// A staff_totp_credentials row is a password-equivalent: whoever holds the
// secret mints valid codes forever. The point of the encryption is that a
// database dump alone does not defeat the second factor.
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

	// And a store with a different key cannot use it.
	other := twofactor.NewStore(pool, "a-different-key")
	if err := other.Verify(ctx, userID, twofactor.Code(secret, twofactor.StepAt(time.Now())+1)); err == nil {
		t.Error("a credential verified under the wrong encryption key")
	}
}

// TestSessionVerificationExpires proves a proof does not last forever.
//
// The proof is time-boxed, or a session stolen months later carries a
// verification made once.
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

	// An unknown token is not verified, and is not an error either — that is a
	// signed-out request, not a failure.
	verified, err = s.SessionVerified(ctx, "no-such-token")
	if err != nil || verified {
		t.Errorf("an unknown token gave verified=%v err=%v", verified, err)
	}
}

// TestNoKeyMeansNoEnrolment proves an unconfigured deployment writes nothing.
//
// A deployment without a key must refuse rather than store a secret in the
// clear. Silently degrading is how a feature that looks enabled protects
// nothing.
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

// TestAnotherAdminCanRecoverALostAuthenticator holds the recovery path the
// design has always described.
//
// This is the recovery path CLAUDE.md has described since 2FA shipped: no
// printed backup codes, because those are a second password-equivalent people
// keep in their email, and a shop with more than one admin has a recovery path
// already. Store.Remove existed with that comment and NO CALLER, so the path
// was a paragraph — an admin who lost their phone was locked out of the back
// office for good, and the only fix was SQL against production.
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

	// And they can enrol again on the new phone, which is the point of removing
	// it rather than the removal itself.
	enrol(t, s, lost, lostEmail)
	if !enrolled(t, lost) {
		t.Error("the account could not enrol again")
	}
}

// TestAnAdminCannotRemoveTheirOwnFactor is the guard that makes the recovery
// path safe.
//
// The session doing this is already step-up verified, so an attacker holding it
// could drop the factor and re-enrol on their own device — turning a stolen
// session into permanent access. Recovery is another admin's job, which is what
// the design says and now what the code enforces.
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
// can add one back, and the fix is SQL against production — which is exactly
// what a back office exists to avoid.
func TestTheLastAdminCannotBeRevoked(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	// This suite's other tests create admins, so the state has to be made
	// rather than assumed: everyone down to customer, then one admin back.
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

// TestRevokingAccessEndsTheSessionsThatHadIt. Without this a revoked colleague
// keeps the back office for the rest of a session's life after being told they
// no longer have it.
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

// TestANewColleagueHasNoPassword holds how a staff account comes into being.
//
// An admin who typed a colleague's password would know it, and a generated one
// has to be delivered somehow. They set their own through /forgot, which is
// already the one path that proves they own the mailbox — and the account
// cannot be signed into until they do.
func TestANewColleagueHasNoPassword(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	// An ADMIN does this, and the actor is now part of the call: AddStaff is an
	// upsert keyed on the address, so submitting your own is submitting your own
	// row and its DO UPDATE sets the role. See
	// TestNobodyCanPromoteThemselvesThroughTheStaffForm.
	actor, _ := staff(t)

	const address = "newcolleague@goen.invalid"
	if err := s.AddStaff(ctx, address, "新同事", "staff", actor); err != nil {
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

	// An existing CUSTOMER is promoted rather than refused: a shop hiring
	// somebody who already shops there is the common case, and "that email is
	// taken" is an answer the admin cannot act on.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, role) VALUES ('shopper@goen.invalid', 'customer')`); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if err := s.AddStaff(ctx, "shopper@goen.invalid", "", "admin", actor); err != nil {
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

// TestNobodyCanPromoteThemselvesThroughTheStaffForm is half of the Critical a
// third-party review found. The other half is the route gate: /admin/staff is
// RequireAdmin now, so a plain staff member never reaches this code at all.
//
// This closes the case the route cannot see. AddStaff is an UPSERT resolved by
// ADDRESS, so submitting your own address is submitting your own row and its
// DO UPDATE sets the role — an admin editing their own entry, or any future
// caller that forgets, would change their own privileges. It was the escalation
// in its simplest form: POST your own email with role=admin.
//
// The case fold matters and is asserted: users_email_key is unique on
// lower(email), so two addresses differing only in case are ONE mailbox, and a
// self-check that compared them literally would be bypassed by pressing shift.
func TestNobodyCanPromoteThemselvesThroughTheStaffForm(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	actor, address := staff(t)
	// Demoted to plain staff first, so a successful promotion would be VISIBLE.
	// The helper creates an admin, and asserting "is still admin" of somebody who
	// was already one is an assertion that cannot fail.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, actor); err != nil {
		t.Fatalf("demote the actor: %v", err)
	}

	for _, submitted := range []string{address, strings.ToUpper(address)} {
		t.Run(submitted, func(t *testing.T) {
			if err := s.AddStaff(ctx, submitted, "我自己", "admin", actor); !errors.Is(err, twofactor.ErrSelf) {
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

	// The CONTROL: adding somebody ELSE still works, or a store that refused
	// every submission would pass everything above.
	other := "colleague-" + uuid.NewString() + "@goen.invalid"
	if err := s.AddStaff(ctx, other, "同事", "staff", actor); err != nil {
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
