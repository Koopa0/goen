//go:build integration

package twofactor_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/web"
)

var pool *pgxpool.Pool

var (
	testKey      = []byte("0123456789abcdef0123456789abcdef")
	differentKey = []byte("fedcba9876543210fedcba9876543210")
)

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
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

func twofactorRolePool(t *testing.T, applicationName, role string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse twofactor application pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return execErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open twofactor application pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitForTwofactorLock(t *testing.T, applicationName string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v",
				applicationName, err)
		default:
		}
		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, applicationName).Scan(&waiting)
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never blocked on the intended database lock: %v", applicationName, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func twofactorOperationResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("operation did not finish after its database lock was released")
		return nil
	}
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

func TestTheEnrolmentSecretPageIsNotCompressed(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
	ctx := account.WithUser(t.Context(), account.User{ID: userID, Email: email, Role: "admin"})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/verify/enrol", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	web.Compress(http.HandlerFunc(h.Enrol)).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("Enrol status = %d, want 200; body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want identity", got)
	}
	if got := res.Header().Get("X-Goen-No-Compress"); got != "" {
		t.Errorf("private no-compress marker leaked as %q", got)
	}
	if res.Body.Len() < 1024 {
		t.Fatalf("response is only %d bytes; it would not prove the opt-out", res.Body.Len())
	}
	if !strings.Contains(res.Body.String(), "otpauth://") {
		t.Error("response does not carry the TOTP URI the test is meant to protect")
	}
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
	// staggers enough that later reads see earlier writes, which hides a guard
	// missing from the SQL.
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

func TestRestartingEnrolmentInvalidatesTheOldSecret(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	old := enrol(t, s, userID, email)

	// Through the RECOVERY path, the only way a proved factor is replaced.
	helper, _ := staff(t)
	if err := s.RemoveFactor(ctx, userID, helper); err != nil {
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

	other := twofactor.NewStore(pool, differentKey)
	if err := other.Verify(ctx, userID, twofactor.Code(secret, twofactor.StepAt(time.Now())+1)); err == nil {
		t.Error("a credential verified under the wrong encryption key")
	}
}

// TestACredentialSealedUnderAnotherKeyIsNotAWrongCode makes a key rotation
// legible: no digits can fix a credential the configured key cannot open.
func TestACredentialSealedUnderAnotherKeyIsNotAWrongCode(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	secret := enrol(t, s, userID, email)

	other := twofactor.NewStore(pool, differentKey)
	err := other.Verify(t.Context(), userID,
		twofactor.Code(secret, twofactor.StepAt(time.Now())+1))
	if !errors.Is(err, twofactor.ErrSecretUnreadable) {
		t.Fatalf("Verify under another key = %v, want ErrSecretUnreadable", err)
	}
	if errors.Is(err, twofactor.ErrBadCode) {
		t.Errorf("Verify under another key also reports ErrBadCode: %v", err)
	}
}

// TestAStaleKeyIsExplainedInsteadOfBlamingTheCode covers both handler doors:
// POST redirects to a stable recovery state, while GET must render that same
// state directly because its Enrolled read decrypts before POST can run.
func TestAStaleKeyIsExplainedInsteadOfBlamingTheCode(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	userID, email := staff(t)
	enrol(t, s, userID, email)
	h := twofactor.NewHandler(twofactor.NewStore(pool, differentKey),
		slog.New(slog.DiscardHandler), false)
	user := account.User{ID: userID, Email: email}

	post := httptest.NewRequestWithContext(account.WithUser(t.Context(), user),
		http.MethodPost, "/admin/verify", strings.NewReader("code=123456"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postOut := httptest.NewRecorder()
	h.Verify(postOut, post)
	if postOut.Code != http.StatusSeeOther || postOut.Header().Get("Location") != "/admin/verify?stale=1" {
		t.Errorf("Verify = %d Location %q, want 303 stale recovery",
			postOut.Code, postOut.Header().Get("Location"))
	}

	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "這組驗證器已無法讀取"},
		{i18n.En, "This authenticator can no longer be read"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(account.WithUser(t.Context(), user), tt.locale)
			get := httptest.NewRequestWithContext(ctx, http.MethodGet,
				"/admin/verify", http.NoBody)
			out := httptest.NewRecorder()
			h.Challenge(out, get)
			if out.Code != http.StatusOK {
				t.Fatalf("Challenge = %d, want 200; body=%s", out.Code, out.Body.String())
			}
			if !strings.Contains(out.Body.String(), tt.want) {
				t.Errorf("Challenge in %s omitted %q; body=%s", tt.locale, tt.want, out.Body.String())
			}
		})
	}
}

// TestDatabaseFailuresAreNotReportedAsWrongCodes keeps an operational failure
// out of the user-correctable branch. Retrying different digits cannot repair a
// failed security-state write, and calling it a bad code hides the incident.
func TestDatabaseFailuresAreNotReportedAsWrongCodes(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)

	t.Run("step-up verification", func(t *testing.T) {
		userID, email := staff(t)
		secret := enrol(t, s, userID, email)
		forceTOTPUpdateFailure(t, userID)

		code := twofactor.Code(secret, twofactor.StepAt(time.Now())+1)
		assertTOTPHandlerFailure(t, account.User{ID: userID, Email: email}, code, h.Verify)
	})

	t.Run("enrolment confirmation", func(t *testing.T) {
		userID, email := staff(t)
		secret, _, err := s.Begin(t.Context(), userID, email)
		if err != nil {
			t.Fatalf("begin enrolment: %v", err)
		}
		forceTOTPUpdateFailure(t, userID)

		code := twofactor.Code(secret, twofactor.StepAt(time.Now()))
		assertTOTPHandlerFailure(t, account.User{ID: userID, Email: email}, code, h.Confirm)
	})
}

func assertTOTPHandlerFailure(
	t *testing.T,
	user account.User,
	code string,
	handle func(http.ResponseWriter, *http.Request),
) {
	t.Helper()
	req := httptest.NewRequestWithContext(account.WithUser(t.Context(), user),
		http.MethodPost, "/admin/verify", strings.NewReader("code="+code))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	out := httptest.NewRecorder()
	handle(out, req)
	if out.Code != http.StatusInternalServerError {
		t.Errorf("handler status = %d Location %q, want 500 without a bad-code redirect",
			out.Code, out.Header().Get("Location"))
	}
}

func forceTOTPUpdateFailure(t *testing.T, userID string) {
	t.Helper()
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_totp_update_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_totp_update_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.user_id = '%s'::uuid THEN
				RAISE EXCEPTION USING
					MESSAGE = 'forced totp state write failure',
					ERRCODE = 'check_violation';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE UPDATE ON staff_totp_credentials
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, userID, triggerName, functionName)); err != nil {
		t.Fatalf("install totp update failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON staff_totp_credentials; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})
}

// TestConfirmDoesNotClaimEnrolmentSuccessWhenMarkVerifiedFails: the factor can
// stay committed, but a failed session stamp is a fault, not /admin?enrolled=1.
func TestConfirmDoesNotClaimEnrolmentSuccessWhenMarkVerifiedFails(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
	userID, email := staff(t)
	secret, _, err := s.Begin(t.Context(), userID, email)
	if err != nil {
		t.Fatalf("begin enrolment: %v", err)
	}

	token := "confirm-mark-verified-fails-" + userID
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256($1::bytea), $2, now() + interval '1 day')`,
		[]byte(token), uuid.MustParse(userID)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	forceSessionMarkFailure(t, userID)

	out := postConfirm(t, h, account.User{ID: userID, Email: email},
		twofactor.Code(secret, twofactor.StepAt(time.Now())), token)
	if out.Code != http.StatusInternalServerError {
		t.Errorf("Confirm = %d Location %q, want 500 without ?enrolled=1",
			out.Code, out.Header().Get("Location"))
	}
	if loc := out.Header().Get("Location"); strings.Contains(loc, "enrolled=1") {
		t.Errorf("Confirm Location = %q; MarkVerified failure must not claim enrolment success", loc)
	}

	enrolled, enrolErr := s.Enrolled(t.Context(), userID)
	if enrolErr != nil {
		t.Fatalf("enrolled: %v", enrolErr)
	}
	if !enrolled {
		t.Error("the factor was rolled back after a failed session stamp")
	}
	verified, verifyErr := s.SessionVerified(t.Context(), token)
	if verifyErr != nil {
		t.Fatalf("session verified: %v", verifyErr)
	}
	if verified {
		t.Error("the session was marked verified even though MarkVerified failed")
	}
}

// TestConfirmWithoutASessionCookieIsNotEnrolmentSuccess: a missing cookie is a
// signed-out request. Enrolment can stay committed; the 303 is not success.
func TestConfirmWithoutASessionCookieIsNotEnrolmentSuccess(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
	userID, email := staff(t)
	secret, _, err := s.Begin(t.Context(), userID, email)
	if err != nil {
		t.Fatalf("begin enrolment: %v", err)
	}

	out := postConfirm(t, h, account.User{ID: userID, Email: email},
		twofactor.Code(secret, twofactor.StepAt(time.Now())), "")
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/signin" {
		t.Errorf("Confirm = %d Location %q, want 303 /signin",
			out.Code, out.Header().Get("Location"))
	}
	if loc := out.Header().Get("Location"); strings.Contains(loc, "enrolled=1") {
		t.Errorf("Confirm Location = %q; a missing cookie must not claim enrolment success", loc)
	}

	enrolled, enrolErr := s.Enrolled(t.Context(), userID)
	if enrolErr != nil {
		t.Fatalf("enrolled: %v", enrolErr)
	}
	if !enrolled {
		t.Error("the factor was rolled back when the session cookie was missing")
	}
}

func postConfirm(
	t *testing.T,
	h *twofactor.Handler,
	user account.User,
	code, token string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(account.WithUser(t.Context(), user),
		http.MethodPost, "/admin/verify/confirm", strings.NewReader("code="+code))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.AddCookie(&http.Cookie{ //nolint:gosec // G124: request cookie, not a Set-Cookie
			Name: "goen_session", Value: token,
		})
	}
	out := httptest.NewRecorder()
	h.Confirm(out, req)
	return out
}

func forceSessionMarkFailure(t *testing.T, userID string) {
	t.Helper()
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_session_mark_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_session_mark_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.user_id = '%s'::uuid THEN
				RAISE EXCEPTION USING
					MESSAGE = 'forced mark session verified failure',
					ERRCODE = 'check_violation';
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE UPDATE ON sessions
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, userID, triggerName, functionName)); err != nil {
		t.Fatalf("install session mark failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON sessions; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})
}

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

func TestNoKeyMeansNoEnrolment(t *testing.T) {
	s := twofactor.NewStore(pool, nil)
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

// TestAnUnkeyedDeploymentSaysSoInsteadOf500 holds the page state an
// unconfigured deployment must reach: an empty key disables the factor the way
// an empty Stripe key disables payment — off and saying so. Enrolled surfaces
// ErrDisabled, which is not ErrSecretUnreadable, so a handler that takes its
// generic failure branch answers 500 and never renders pages.TwoFactor's
// !Enabled branch, whose whole job is to name the missing GOEN_TOTP_KEY.
func TestAnUnkeyedDeploymentSaysSoInsteadOf500(t *testing.T) {
	userID, email := staff(t)
	h := twofactor.NewHandler(twofactor.NewStore(pool, nil), slog.New(slog.DiscardHandler), false)

	ctx := account.WithUser(t.Context(), account.User{ID: userID, Email: email, Role: "admin"})
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/verify", http.NoBody)
	out := httptest.NewRecorder()
	h.Challenge(out, req)

	if out.Code != http.StatusOK {
		t.Fatalf("Challenge = %d, want 200; body = %q", out.Code, out.Body.String())
	}
	// The status is not the lock on its own: a page rendered with Enabled
	// hardcoded true keeps the 200 and loses the one sentence that tells an
	// operator which variable is missing.
	if want := i18n.T(ctx, i18n.KeyTwoFAOffBody); !strings.Contains(out.Body.String(), want) {
		t.Errorf("the off-state notice is missing; want %q in the body", want)
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

	only, _ := staff(t)
	// This suite's other tests create admins, so leave exactly the intended one.
	// The database invariant correctly refuses a transition all the way to zero.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1`, only); err != nil {
		t.Fatalf("leave one admin: %v", err)
	}
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
	// A SECOND admin, because the demotion below is refused outright when the
	// actor is the only one — users_last_admin is a database rule, not a Go
	// check. Which admins exist is shared state and the suite shuffles, so a
	// fixture relying on another test's leftover admin passes only in some
	// orderings.
	staff(t)
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
// asks about the ADDRESS rather than about promotion: a verified address
// provably belongs to whoever reads that mailbox, which is the person being
// hired. Clearing their password would be friction bought with nothing, in the
// common case of a shop hiring somebody who already shops there.
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

// TestPromotionEndsTheSessionsOpenedBeforeIt holds the half the credential test
// cannot see: secure_promoted_account ends sessions ALWAYS, and not only when
// it cleared an unproved password.
//
// role is read live on every request, so a session opened before the promotion
// becomes a back-office session the moment the role moves: one the shop had not
// yet decided to trust with /admin, on whatever machine it was left open.
func TestPromotionEndsTheSessionsOpenedBeforeIt(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	actor, _ := staff(t)

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

	if _, err := s.AddStaff(ctx, address, "老顧客", "staff", actor); err != nil {
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

// TestRecoveringAFactorRollsBackWhenSessionRevocationFails proves the factor
// and the sessions are one security change. A failure after deleting only the
// factor would leave the exact stepped-up session recovery exists to end.
func TestRecoveringAFactorRollsBackWhenSessionRevocationFails(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	victim, victimEmail := staff(t)
	other, _ := staff(t)
	enrol(t, s, victim, victimEmail)
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

	err := s.RemoveFactor(ctx, victim, other)
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

// TestErasureAndDemotionShareTheRosterGuard covers the cross-feature race:
// erasing A and changing B from admin to staff must not each observe the other
// as the remaining administrator. Both application roles enter through their
// real database doors and are held at the shared guard before either can write.
func TestErasureAndDemotionShareTheRosterGuard(t *testing.T) {
	ctx := t.Context()
	a, _ := staff(t)
	b, bEmail := staff(t)
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
	eraseName, demoteName := "erase-demote-erase-"+suffix, "erase-demote-role-"+suffix
	eraser := account.NewStore(twofactorRolePool(t, eraseName, "store"))
	staffStore := twofactor.NewStore(twofactorRolePool(t, demoteName, "admin"), testKey)
	eraseDone, demoteDone := make(chan error, 1), make(chan error, 1)
	go func() { eraseDone <- eraser.Erase(context.WithoutCancel(ctx), a) }()
	go func() {
		_, demoteErr := staffStore.AddStaff(
			context.WithoutCancel(ctx), bEmail, "測試", "staff", a)
		demoteDone <- demoteErr
	}()
	waitForTwofactorLock(t, eraseName, eraseDone)
	waitForTwofactorLock(t, demoteName, demoteDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release roster blocker: %v", err)
	}
	eraseErr := twofactorOperationResult(t, eraseDone)
	demoteErr := twofactorOperationResult(t, demoteDone)
	eraseRefused := func(err error) bool {
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		return ok && pgErr.ConstraintName == "erase_user_keeps_one_admin"
	}
	eraseWon := eraseErr == nil && errors.Is(demoteErr, twofactor.ErrLastAdmin)
	demoteWon := demoteErr == nil && eraseRefused(eraseErr)
	if !eraseWon && !demoteWon {
		t.Fatalf("erase/demote = %v / %v, want one success and one last-admin refusal",
			eraseErr, demoteErr)
	}

	var admins int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if admins != 1 {
		t.Errorf("erase/demote left %d admins, want 1", admins)
	}
}

func staffAuditCount(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", action, err)
	}
	return n
}

func readStaffAudit(t *testing.T, action, target string) (actor, email, role string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT actor_user_id::text, coalesce(after->>'email', ''), coalesce(after->>'role', '')
		FROM audit_events
		WHERE action = $1 AND entity_id = $2
		ORDER BY occurred_at DESC, id DESC LIMIT 1`, action, target).
		Scan(&actor, &email, &role); err != nil {
		t.Fatalf("read %s audit for %s: %v", action, target, err)
	}
	return actor, email, role
}

// TestStaffAuthorizationChangesLeaveATrail is the lock for #122: each staff
// form that actually changes authorization or a factor writes one audit row
// naming the actor and the target. A drop of the record call is a silent
// back-office grant.
func TestStaffAuthorizationChangesLeaveATrail(t *testing.T) {
	ctx := web.WithRequestID(t.Context(), "req-staff-audit")
	s := twofactor.NewStore(pool, testKey)
	actor, _ := staff(t)

	address := "audited-" + uuid.NewString()[:8] + "@goen.invalid"
	beforeGrant := staffAuditCount(t, "staff.grant")
	if _, err := s.AddStaff(ctx, address, "稽核同事", "staff", actor); err != nil {
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
	if err := s.RevokeStaff(ctx, target, actor); err != nil {
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

	lost, lostEmail := staff(t)
	enrol(t, s, lost, lostEmail)
	beforeRemove := staffAuditCount(t, "staff.factor.remove")
	if err := s.RemoveFactor(ctx, lost, actor); err != nil {
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
	s := twofactor.NewStore(pool, testKey)
	self, selfEmail := staff(t)
	helper, _ := staff(t)

	beforeGrant := staffAuditCount(t, "staff.grant")
	if _, err := s.AddStaff(ctx, selfEmail, "我自己", "admin", self); !errors.Is(err, twofactor.ErrSelf) {
		t.Fatalf("self AddStaff = %v, want ErrSelf", err)
	}
	if _, err := s.AddStaff(ctx, "not-an-email", "無效", "staff", self); !errors.Is(err, twofactor.ErrInvalidStaff) {
		t.Fatalf("invalid AddStaff = %v, want ErrInvalidStaff", err)
	}
	if after := staffAuditCount(t, "staff.grant"); after != beforeGrant {
		t.Errorf("refused AddStaff left %d staff.grant rows, want %d", after, beforeGrant)
	}

	beforeRevoke := staffAuditCount(t, "staff.revoke")
	if err := s.RevokeStaff(ctx, self, self); !errors.Is(err, twofactor.ErrSelf) {
		t.Fatalf("self RevokeStaff = %v, want ErrSelf", err)
	}
	if after := staffAuditCount(t, "staff.revoke"); after != beforeRevoke {
		t.Errorf("self RevokeStaff left %d staff.revoke rows, want %d", after, beforeRevoke)
	}

	enrol(t, s, self, selfEmail)
	beforeRemove := staffAuditCount(t, "staff.factor.remove")
	if err := s.RemoveFactor(ctx, self, self); !errors.Is(err, twofactor.ErrSelf) {
		t.Fatalf("self RemoveFactor = %v, want ErrSelf", err)
	}
	missing := uuid.NewString()
	if err := s.RemoveFactor(ctx, missing, helper); !errors.Is(err, twofactor.ErrNotEnrolled) {
		t.Fatalf("RemoveFactor on nobody = %v, want ErrNotEnrolled", err)
	}
	if after := staffAuditCount(t, "staff.factor.remove"); after != beforeRemove {
		t.Errorf("refused RemoveFactor left %d staff.factor.remove rows, want %d", after, beforeRemove)
	}

	only, _ := staff(t)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1`, only); err != nil {
		t.Fatalf("leave one admin: %v", err)
	}
	other, _ := staff(t)
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, other); err != nil {
		t.Fatalf("demote the helper: %v", err)
	}
	beforeLast := staffAuditCount(t, "staff.revoke")
	if err := s.RevokeStaff(ctx, only, other); !errors.Is(err, twofactor.ErrLastAdmin) {
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
	s := twofactor.NewStore(pool, testKey)
	missing := uuid.NewString()
	address := "unattributed-" + uuid.NewString()[:8] + "@goen.invalid"

	_, err := s.AddStaff(ctx, address, "不得落地", "staff", missing)
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
