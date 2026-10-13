//go:build integration

package twofactor_test

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/twofactor"
	"github.com/koopa0/goen/internal/user"
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

// enrol takes a user all the way to a confirmed credential.
// asActor is the context the access wrapper leaves for a signed-in admin.
func asActor(ctx context.Context, id string) context.Context {
	return user.NewContext(ctx, user.User{ID: id, Role: user.RoleAdmin})
}

func enrol(t *testing.T, s *twofactor.Store, userID, email string) []byte {
	t.Helper()
	secret, _, err := s.Begin(t.Context(), userID, email)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	code := twofactor.Code(secret, twofactor.StepAt(time.Now()))
	if err := s.Confirm(t.Context(), userID, code, mailedCode(t, email)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return secret
}

// mailedCode is the code the latest enrolment for address queued to it.
func mailedCode(t *testing.T, address string) string {
	t.Helper()
	var code string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'code' FROM outbox_messages
		WHERE topic = $1 AND payload->>'email' = $2
		ORDER BY id DESC LIMIT 1`,
		outbox.TopicStaffEnrolment.Name(), address).Scan(&code); err != nil {
		t.Fatalf("read the mailed enrolment code: %v", err)
	}
	return code
}

func TestTheEnrolmentSecretPageIsNotCompressed(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	userID, email := admintest.AdminUser(t, pool)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
	ctx := user.NewContext(t.Context(), user.User{ID: userID, Email: email, Role: user.RoleAdmin})
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
	userID, email := admintest.AdminUser(t, pool)
	secret := enrol(t, s, userID, email)

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

	q := db.New(pool)
	claim := db.RecordTOTPStepParams{UserID: uuid.MustParse(userID), Step: step}
	const racers = 8
	start := make(chan struct{})
	results := make([]struct {
		affected int64
		err      error
	}, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			results[i].affected, results[i].err = q.RecordTOTPStep(ctx, claim)
		})
	}
	close(start)
	wg.Wait()

	var wins int64
	for i, result := range results {
		if result.err != nil {
			t.Errorf("claim %d of step %d: %v", i, step, result.err)
		}
		wins += result.affected
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
	userID, email := admintest.AdminUser(t, pool)
	old := enrol(t, s, userID, email)

	// Through the RECOVERY path, the only way a proved factor is replaced.
	helper, _ := admintest.AdminUser(t, pool)
	if err := staff.NewStore(pool).RemoveFactor(asActor(ctx, helper), userID); err != nil {
		t.Fatalf("remove the old factor: %v", err)
	}

	fresh, _, beginErr := s.Begin(ctx, userID, email)
	if beginErr != nil {
		t.Fatalf("re-begin: %v", beginErr)
	}

	step := twofactor.StepAt(time.Now()) + 1
	if err := s.Confirm(ctx, userID, twofactor.Code(old, step), mailedCode(t, email)); err == nil {
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

	if err := s.Confirm(ctx, userID, twofactor.Code(fresh, step), mailedCode(t, email)); err != nil {
		t.Errorf("a code from the new secret was refused: %v", err)
	}
	if enrolled, enrolErr = s.Enrolled(ctx, userID); enrolErr != nil || !enrolled {
		t.Errorf("confirming the new secret did not enrol (err=%v)", enrolErr)
	}
}

// TestEnrolmentIsConfirmedOnlyWithTheCodeMailedToTheAccount: a code from the
// new authenticator does not finish enrolment without the one mailed to the
// account's address.
func TestEnrolmentIsConfirmedOnlyWithTheCodeMailedToTheAccount(t *testing.T) {
	s := twofactor.NewStore(pool, testKey)
	h := twofactor.NewHandler(s, slog.New(slog.DiscardHandler), false)
	userID, email := admintest.AdminUser(t, pool)
	u := user.User{ID: userID, Email: email, Role: user.RoleAdmin}

	req := httptest.NewRequestWithContext(user.NewContext(t.Context(), u),
		http.MethodPost, "/admin/verify/enrol", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	enrolment := httptest.NewRecorder()
	h.Enrol(enrolment, req)
	if enrolment.Code != http.StatusOK {
		t.Fatalf("Enrol = %d, want 200; body=%s", enrolment.Code, enrolment.Body.String())
	}
	secret := secretOnPage(t, enrolment.Body.String())
	mailed := mailedCode(t, email)
	if len(mailed) != 8 {
		t.Fatalf("mailed code %q, want eight digits", mailed)
	}
	if strings.Contains(enrolment.Body.String(), mailed) {
		t.Fatal("the enrolment page shows the code it mailed")
	}

	token := "mailed-code-" + userID
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (sha256($1::bytea), $2, now() + interval '1 day')`,
		[]byte(token), uuid.MustParse(userID)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	code := twofactor.Code(secret, twofactor.StepAt(time.Now()))

	wrong := []byte(mailed)
	wrong[7] = '0' + (wrong[7]-'0'+1)%10
	for _, tt := range []struct{ name, mailed string }{
		{"no mailed code", ""},
		{"a wrong mailed code", string(wrong)},
	} {
		out := postConfirm(t, h, u, code, tt.mailed, token)
		if loc := out.Header().Get("Location"); out.Code != http.StatusSeeOther || loc != "/admin/verify?badenrol=1" {
			t.Errorf("Confirm with %s = %d Location %q, want 303 /admin/verify?badenrol=1", tt.name, out.Code, loc)
		}
		if enrolled, err := s.Enrolled(t.Context(), userID); err != nil || enrolled {
			t.Errorf("Confirm with %s left Enrolled = %v (err=%v), want false", tt.name, enrolled, err)
		}
		if verified, err := s.SessionVerified(t.Context(), token); err != nil || verified {
			t.Errorf("Confirm with %s left SessionVerified = %v (err=%v), want false", tt.name, verified, err)
		}
	}

	out := postConfirm(t, h, u, code, mailed, token)
	if loc := out.Header().Get("Location"); out.Code != http.StatusSeeOther || loc != "/admin?enrolled=1" {
		t.Errorf("Confirm with the mailed code = %d Location %q, want 303 /admin?enrolled=1", out.Code, loc)
	}
	if verified, err := s.SessionVerified(t.Context(), token); err != nil || !verified {
		t.Errorf("Confirm with the mailed code left SessionVerified = %v (err=%v), want true", verified, err)
	}
}

func TestAMailedCodeIsFreshLatestAndSingleUse(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)

	t.Run("spent", func(t *testing.T) {
		userID, email := admintest.AdminUser(t, pool)
		secret := enrol(t, s, userID, email)
		later := twofactor.Code(secret, twofactor.StepAt(time.Now())+1)
		if err := s.Confirm(ctx, userID, later, mailedCode(t, email)); !errors.Is(err, twofactor.ErrBadCode) {
			t.Errorf("Confirm with a mailed code already spent = %v, want ErrBadCode", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		userID, email := admintest.AdminUser(t, pool)
		secret, _, err := s.Begin(ctx, userID, email)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			UPDATE staff_totp_credentials SET created_at = now() - make_interval(secs => $2)
			WHERE user_id = $1`, uuid.MustParse(userID), (twofactor.MailedCodeTTL + time.Minute).Seconds()); err != nil {
			t.Fatalf("age the enrolment: %v", err)
		}
		code := twofactor.Code(secret, twofactor.StepAt(time.Now()))
		if err := s.Confirm(ctx, userID, code, mailedCode(t, email)); !errors.Is(err, twofactor.ErrBadCode) {
			t.Errorf("Confirm %v after the mailed code expired = %v, want ErrBadCode", twofactor.MailedCodeTTL+time.Minute, err)
		}
	})

	t.Run("replaced by a restart", func(t *testing.T) {
		userID, email := admintest.AdminUser(t, pool)
		if _, _, err := s.Begin(ctx, userID, email); err != nil {
			t.Fatalf("begin: %v", err)
		}
		first := mailedCode(t, email)
		secret, _, err := s.Begin(ctx, userID, email)
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
		latest := mailedCode(t, email)
		if latest == first {
			t.Skip("both enrolments drew the same code")
		}
		code := twofactor.Code(secret, twofactor.StepAt(time.Now()))
		if err := s.Confirm(ctx, userID, code, first); !errors.Is(err, twofactor.ErrBadCode) {
			t.Errorf("Confirm with the code mailed before the restart = %v, want ErrBadCode", err)
		}
		if err := s.Confirm(ctx, userID, code, latest); err != nil {
			t.Errorf("Confirm with the latest mailed code = %v, want nil", err)
		}
	})
}

// secretOnPage reads the secret from the provisioning URI an enrolment page shows.
func secretOnPage(t *testing.T, body string) []byte {
	t.Helper()
	m := regexp.MustCompile(`<code class="goen-twofa__uri">([^<]+)</code>`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatal("the enrolment page shows no provisioning URI")
	}
	uri, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatalf("parse provisioning URI: %v", err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(uri.Query().Get("secret"))
	if err != nil {
		t.Fatalf("decode the secret: %v", err)
	}
	return secret
}

// restartAfterRead runs restart once, right after the credential has been read:
// the point between Confirm's read and its write that a second enrolment tab
// can reach.
type restartAfterRead struct {
	once    sync.Once
	restart func()
}

func (*restartAfterRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (r *restartAfterRead) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if strings.Contains(data.CommandTag.String(), "SELECT") && data.Err == nil {
		r.once.Do(r.restart)
	}
}

func TestConfirmOnlyConfirmsTheSecretItCheckedTheCodeAgainst(t *testing.T) {
	ctx := t.Context()
	userID, email := admintest.AdminUser(t, pool)
	other := twofactor.NewStore(pool, testKey)
	first, _, err := other.Begin(ctx, userID, email)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// The restart keeps the first mailed code, so only the secret can tell the
	// two enrolments apart.
	mailed := mailedCode(t, email)
	var second []byte
	tracer := &restartAfterRead{}
	tracer.restart = func() {
		var beginErr error
		if second, _, beginErr = other.Begin(ctx, userID, email); beginErr != nil {
			t.Errorf("restart enrolment: %v", beginErr)
		}
		if _, execErr := pool.Exec(ctx, `
			UPDATE staff_totp_credentials SET mailed_code_hash = sha256(convert_to($2, 'UTF8'))
			WHERE user_id = $1`, uuid.MustParse(userID), mailed); execErr != nil {
			t.Errorf("keep the first mailed code: %v", execErr)
		}
	}
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer tracedPool.Close()
	s := twofactor.NewStore(tracedPool, testKey)

	step := twofactor.StepAt(time.Now())
	if err = s.Confirm(ctx, userID, twofactor.Code(first, step), mailed); err == nil {
		t.Error("Confirm accepted a code for the first secret after enrolment restarted with a second")
	}
	enrolled, err := s.Enrolled(ctx, userID)
	if err != nil {
		t.Fatalf("enrolled: %v", err)
	}
	if enrolled {
		t.Error("the second secret is confirmed though only a code for the first was checked")
	}

	if err = s.Confirm(ctx, userID, twofactor.Code(second, step), mailed); err != nil {
		t.Errorf("a code for the second secret was refused: %v", err)
	}
}

// TestAConfirmedFactorCannotBeReplacedByItsOwnHolder proves a stolen password
// alone cannot swap the second factor for the attacker's own device.
func TestAConfirmedFactorCannotBeReplacedByItsOwnHolder(t *testing.T) {
	ctx := t.Context()
	s := twofactor.NewStore(pool, testKey)
	userID, email := admintest.AdminUser(t, pool)
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
	other, otherEmail := admintest.AdminUser(t, pool)
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
	userID, email := admintest.AdminUser(t, pool)

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
	userID, email := admintest.AdminUser(t, pool)
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
	userID, email := admintest.AdminUser(t, pool)
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
	userID, email := admintest.AdminUser(t, pool)
	enrol(t, s, userID, email)
	h := twofactor.NewHandler(twofactor.NewStore(pool, differentKey),
		slog.New(slog.DiscardHandler), false)
	u := user.User{ID: userID, Email: email}

	post := httptest.NewRequestWithContext(user.NewContext(t.Context(), u),
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
			ctx := i18n.WithLocale(user.NewContext(t.Context(), u), tt.locale)
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
		userID, email := admintest.AdminUser(t, pool)
		secret := enrol(t, s, userID, email)
		forceTOTPUpdateFailure(t, userID)

		form := url.Values{"code": {twofactor.Code(secret, twofactor.StepAt(time.Now())+1)}}
		assertTOTPHandlerFailure(t, user.User{ID: userID, Email: email}, form, h.Verify)
	})

	t.Run("enrolment confirmation", func(t *testing.T) {
		userID, email := admintest.AdminUser(t, pool)
		secret, _, err := s.Begin(t.Context(), userID, email)
		if err != nil {
			t.Fatalf("begin enrolment: %v", err)
		}
		forceTOTPUpdateFailure(t, userID)

		form := url.Values{
			"code":        {twofactor.Code(secret, twofactor.StepAt(time.Now()))},
			"mailed_code": {mailedCode(t, email)},
		}
		assertTOTPHandlerFailure(t, user.User{ID: userID, Email: email}, form, h.Confirm)
	})
}

func assertTOTPHandlerFailure(
	t *testing.T,
	u user.User,
	form url.Values,
	handle func(http.ResponseWriter, *http.Request),
) {
	t.Helper()
	req := httptest.NewRequestWithContext(user.NewContext(t.Context(), u),
		http.MethodPost, "/admin/verify", strings.NewReader(form.Encode()))
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
	userID, email := admintest.AdminUser(t, pool)
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

	out := postConfirm(t, h, user.User{ID: userID, Email: email},
		twofactor.Code(secret, twofactor.StepAt(time.Now())), mailedCode(t, email), token)
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
	userID, email := admintest.AdminUser(t, pool)
	secret, _, err := s.Begin(t.Context(), userID, email)
	if err != nil {
		t.Fatalf("begin enrolment: %v", err)
	}

	out := postConfirm(t, h, user.User{ID: userID, Email: email},
		twofactor.Code(secret, twofactor.StepAt(time.Now())), mailedCode(t, email), "")
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
	u user.User,
	code, mailed, token string,
) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"code": {code}, "mailed_code": {mailed}}
	req := httptest.NewRequestWithContext(user.NewContext(t.Context(), u),
		http.MethodPost, "/admin/verify/confirm", strings.NewReader(form.Encode()))
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
	userID, _ := admintest.AdminUser(t, pool)

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
	userID, email := admintest.AdminUser(t, pool)

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
// !Enabled branch, whose whole job is to say the factor is off.
func TestAnUnkeyedDeploymentSaysSoInsteadOf500(t *testing.T) {
	userID, email := admintest.AdminUser(t, pool)
	h := twofactor.NewHandler(twofactor.NewStore(pool, nil), slog.New(slog.DiscardHandler), false)

	ctx := user.NewContext(t.Context(), user.User{ID: userID, Email: email, Role: user.RoleAdmin})
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/verify", http.NoBody)
	out := httptest.NewRecorder()
	h.Challenge(out, req)

	if out.Code != http.StatusOK {
		t.Fatalf("Challenge = %d, want 200; body = %q", out.Code, out.Body.String())
	}
	// The status is not the lock on its own: a page rendered with Enabled
	// hardcoded true keeps the 200 and loses the one sentence that tells an
	// operator the factor is off.
	if want := i18n.T(ctx, i18n.KeyTwoFAOffBody); !strings.Contains(out.Body.String(), want) {
		t.Errorf("the off-state notice is missing; want %q in the body", want)
	}
}
