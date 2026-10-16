//go:build integration

package auth_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// plantLiveResetToken writes an unused token without going through beginReset.
// Issuing a replacement spends every earlier unused row, so a fixture that
// needs two live siblings has to write those rows itself.
func plantLiveResetToken(t *testing.T, userID string) string {
	t.Helper()
	token := bearer.New()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO password_reset_tokens (digest, user_id, expires_at)
		VALUES ($1, $2, now() + interval '1 hour')`,
		bearer.Digest(token), uuid.MustParse(userID)); err != nil {
		t.Fatalf("plant live reset token: %v", err)
	}
	return token
}

func TestAResetTokenIsSpentExactlyOnce(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	register(t, s, "spendonce@example.com")

	token := beginReset(t, s, "spendonce@example.com")

	const racers = 8
	start := make(chan struct{})
	results := make(chan error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			// A different password per racer, so the survivor is identifiable.
			_, err := s.CompleteReset(ctx, token, "racer password number "+strconv.Itoa(i))
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, auth.ErrResetInvalid):
		default:
			t.Errorf("a racer failed for the wrong reason: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("the token was spent %d times, want exactly 1", won)
	}
}

func TestAnExpiredResetTokenIsRefused(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	register(t, s, "expiredreset@example.com")

	token := beginReset(t, s, "expiredreset@example.com")
	// created_at moves with it: password_reset_tokens_expiry_after_creation
	// refuses a row whose window closed before it opened.
	digest := sha256.Sum256([]byte(token))
	tag, err := pool.Exec(ctx,
		`UPDATE password_reset_tokens
		 SET created_at = now() - interval '2 hours',
		     expires_at = now() - interval '1 second'
		 WHERE digest = $1`, digest[:])
	if err != nil {
		t.Fatalf("age the token: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("aged %d rows, want 1 — the token was not stored where this test looks",
			tag.RowsAffected())
	}

	// Distinct from the password register() sets, or the second assertion proves nothing.
	const attempted = "the password an expired link tried to set"
	if _, err := s.CompleteReset(ctx, token, attempted); !errors.Is(err, auth.ErrResetInvalid) {
		t.Errorf("an expired token was accepted: %v", err)
	}
	if _, err := s.Authenticate(ctx, "expiredreset@example.com", attempted); err == nil {
		t.Error("the password changed anyway")
	}
}

func TestAReplacementResetLinkInvalidatesTheEarlierToken(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "replace-reset-" + uuid.NewString() + "@example.com"
	u := register(t, s, email)

	first := beginReset(t, s, email)
	second := beginReset(t, s, email)
	if first == second {
		t.Fatal("two requests produced the same token")
	}

	var live int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens
		WHERE user_id = $1 AND used_at IS NULL`,
		uuid.MustParse(u.ID)).Scan(&live); err != nil {
		t.Fatalf("count unused reset tokens: %v", err)
	}
	if live != 1 {
		t.Fatalf("unused reset tokens after a replacement = %d, want 1", live)
	}

	const attempted = "password the replaced link must not set"
	if _, err := s.CompleteReset(ctx, first, attempted); !errors.Is(err, auth.ErrResetInvalid) {
		t.Errorf("the replaced link still works: %v", err)
	}
	if _, err := s.Authenticate(ctx, email, attempted); err == nil {
		t.Error("the replaced link changed the password anyway")
	}
	if _, err := s.CompleteReset(ctx, second, "the newly chosen password"); err != nil {
		t.Fatalf("the latest link failed: %v", err)
	}
	if _, err := s.Authenticate(ctx, email, "the newly chosen password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestAResetInvalidatesSiblingTokensAndSessions(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "siblings@example.com")

	stolen, err := s.StartSession(ctx, u.ID, "thief", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	first := plantLiveResetToken(t, u.ID)
	second := plantLiveResetToken(t, u.ID)
	if first == second {
		t.Fatal("two planted tokens collided")
	}

	if _, err := s.CompleteReset(ctx, second, "the newly chosen password"); err != nil {
		t.Fatalf("complete reset: %v", err)
	}

	if _, err := s.CompleteReset(ctx, first, "yet another password"); !errors.Is(err, auth.ErrResetInvalid) {
		t.Errorf("the older link still works: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); !errors.Is(err, auth.ErrNotFound) {
		t.Error("a session survived the reset that was meant to end it")
	}
	if _, err := s.Authenticate(ctx, "siblings@example.com", "the newly chosen password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

// TestConcurrentResetIssuanceLeavesOneLiveToken holds the account until both
// replacement requests have reached it. They serialize on the user row so
// exactly one unused token remains, and only that token can complete.
func TestConcurrentResetIssuanceLeavesOneLiveToken(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	email := "replace-race-" + uuid.NewString() + "@example.com"
	u := register(t, s, email)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset-issue blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = blocker.Exec(ctx,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("lock reset account: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "replace-reset-a-"+suffix, "replace-reset-b-"+suffix
	firstStore := auth.NewStore(accountStorePool(t, firstName))
	secondStore := auth.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- auth.BeginReset(context.WithoutCancel(ctx), firstStore, email) }()
	go func() { secondDone <- auth.BeginReset(context.WithoutCancel(ctx), secondStore, email) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err = blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset account: %v", err)
	}
	if err = operationResult(t, firstDone); err != nil {
		t.Fatalf("first replacement issue: %v", err)
	}
	if err = operationResult(t, secondDone); err != nil {
		t.Fatalf("second replacement issue: %v", err)
	}

	var live int
	if err = pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens
		WHERE user_id = $1 AND used_at IS NULL`,
		uuid.MustParse(u.ID)).Scan(&live); err != nil {
		t.Fatalf("count unused reset tokens: %v", err)
	}
	if live != 1 {
		t.Fatalf("unused reset tokens after concurrent issuance = %d, want 1", live)
	}

	var rows pgx.Rows
	rows, err = pool.Query(ctx, `
		SELECT payload->>'token'
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id`, outbox.TopicPasswordReset.Name(), email)
	if err != nil {
		t.Fatalf("list issued reset tokens: %v", err)
	}
	defer rows.Close()
	var issued []string
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			t.Fatalf("read issued reset token: %v", err)
		}
		issued = append(issued, token)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate issued reset tokens: %v", err)
	}
	if len(issued) != 2 {
		t.Fatalf("queued reset tokens = %d, want 2", len(issued))
	}

	var accepted int
	for i, token := range issued {
		_, err = s.CompleteReset(ctx, token, fmt.Sprintf("concurrent reset password %d", i))
		switch {
		case err == nil:
			accepted++
		case !errors.Is(err, auth.ErrResetInvalid):
			t.Fatalf("complete issued token %d: %v", i, err)
		}
	}
	if accepted != 1 {
		t.Fatalf("live tokens that completed = %d, want 1", accepted)
	}
}

// TestConcurrentSiblingResetsSerializeOnTheAccount holds the user row until
// both already-issued sibling links have reached it. Only one token may change
// the password; that winner invalidates the sibling before the second request
// can spend it.
func TestConcurrentSiblingResetsSerializeOnTheAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "sibling-race-"+uuid.NewString()+"@example.com")
	first := plantLiveResetToken(t, u.ID)
	second := plantLiveResetToken(t, u.ID)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = blocker.Exec(ctx,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("lock reset account: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "sibling-reset-a-"+suffix, "sibling-reset-b-"+suffix
	firstStore := auth.NewStore(accountStorePool(t, firstName))
	secondStore := auth.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	const firstPassword = "first sibling reset password"
	const secondPassword = "second sibling reset password"
	go func() {
		_, resetErr := firstStore.CompleteReset(context.WithoutCancel(ctx), first, firstPassword)
		firstDone <- resetErr
	}()
	go func() {
		_, resetErr := secondStore.CompleteReset(context.WithoutCancel(ctx), second, secondPassword)
		secondDone <- resetErr
	}()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err = blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset account: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	oneSucceededAndOneWasInvalid := firstErr == nil && errors.Is(secondErr, auth.ErrResetInvalid) ||
		secondErr == nil && errors.Is(firstErr, auth.ErrResetInvalid)
	if !oneSucceededAndOneWasInvalid {
		t.Fatalf("sibling resets = %v / %v, want one success and one invalid token",
			firstErr, secondErr)
	}
	winnerPassword := firstPassword
	if secondErr == nil {
		winnerPassword = secondPassword
	}
	if _, err := s.Authenticate(ctx, u.Email, winnerPassword); err != nil {
		t.Errorf("winning reset password does not authenticate: %v", err)
	}
}

func TestBeginResetSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	register(t, s, "real@example.com")

	realToken := beginReset(t, s, "real@example.com")
	if realToken == "" {
		t.Fatal("a real address did not queue a reset token")
	}

	for _, address := range []string{"nobody@example.com", "not an address", ""} {
		if err := auth.BeginReset(ctx, s, address); err != nil {
			t.Errorf("BeginReset(%q) errored where it must stay quiet: %v", address, err)
		}
		var queued int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
			outbox.TopicPasswordReset.Name(), address).Scan(&queued); err != nil {
			t.Fatalf("count reset messages for %q: %v", address, err)
		}
		if queued != 0 {
			t.Errorf("BeginReset(%q) queued %d messages for an unknown address", address, queued)
		}
	}
}

func TestAResetTokenIsStoredHashed(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	register(t, s, "hashedreset@example.com")

	token := beginReset(t, s, "hashedreset@example.com")
	var stored []byte
	digest := sha256.Sum256([]byte(token))
	if err := pool.QueryRow(ctx,
		`SELECT digest FROM password_reset_tokens WHERE digest = $1`,
		digest[:]).Scan(&stored); err != nil {
		t.Fatalf("the digest is not what is stored: %v", err)
	}
	var raw int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM password_reset_tokens
		 WHERE encode(digest, 'escape') = $1`, token).Scan(&raw); err != nil {
		t.Fatalf("scan raw count: %v", err)
	}
	if raw != 0 {
		t.Error("the raw token is in the table")
	}
}

func TestAWeakNewPasswordIsRefusedWithoutSpendingTheToken(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	register(t, s, "weakreset@example.com")

	token := beginReset(t, s, "weakreset@example.com")
	if _, err := s.CompleteReset(ctx, token, "short"); !errors.Is(err, auth.ErrInvalidPassword) {
		t.Fatalf("a short password was not refused as such: %v", err)
	}
	if _, err := s.CompleteReset(ctx, token, "a sufficiently long password"); err != nil {
		t.Errorf("the token was burnt by the refused attempt: %v", err)
	}
}

func TestTheSweepDropsDeadResetTokensAndKeepsLiveOnes(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "sweep-"+uuid.NewString()+"@goen.invalid")
	userID := u.ID

	// Four tokens: spent long ago, expired long ago, spent just now, and live.
	plant := func(digest string, used bool, expires, created string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO password_reset_tokens (digest, user_id, expires_at, used_at, created_at)
			VALUES (sha256($1::bytea), $2, now() + $3::interval,
			        CASE WHEN $4 THEN now() ELSE NULL END, now() + $5::interval)`,
			digest, userID, expires, used, created); err != nil {
			t.Fatalf("plant %s: %v", digest, err)
		}
	}
	plant("spent-old", true, "1 hour", "-30 days")
	plant("expired-old", false, "-29 days", "-30 days")
	plant("spent-today", true, "1 hour", "0 days")
	plant("live", false, "1 hour", "0 days")

	if err := s.SweepSessions(ctx); err != nil {
		t.Fatalf("SweepSessions: %v", err)
	}

	for digest, want := range map[string]bool{
		"spent-old": false, "expired-old": false,
		// Inside the grace window: still answerable by support.
		"spent-today": true,
		"live":        true,
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM password_reset_tokens
			               WHERE digest = sha256($1::bytea))`, digest).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", digest, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", digest, exists, want)
		}
	}
}

func TestSuccessfulResetReturnsToThePrivatePrefilledSignIn(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "reset-prefill-"+uuid.NewString()))
	u := registerProved(t, s, "reset-prefill-"+uuid.NewString()+"@example.com")
	token := beginReset(t, s, u.Email)
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	const password = "the newly reset long password"
	reset := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		h.Reset(response, cartForm(ctx, "/reset", url.Values{
			"token": {token}, "password": {password}, "confirm": {password},
		}))
		return response
	}
	response := reset()
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/signin?reset=1" {
		t.Fatalf("successful reset = %d Location %q, want 303 /signin?reset=1", response.Code, response.Header().Get("Location"))
	}
	for _, locale := range i18n.Locales() {
		request := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, response.Header().Get("Location"), http.NoBody)
		for _, cookie := range response.Result().Cookies() {
			request.AddCookie(cookie)
		}
		page := httptest.NewRecorder()
		h.SignInPage(page, request)
		assertSignInPrefill(t, page.Body.String(), u.Email, false)
	}
	if _, err := s.Authenticate(ctx, u.Email, password); err != nil {
		t.Errorf("reset password cannot authenticate: %v", err)
	}
	if _, err := s.Authenticate(ctx, u.Email, "a sufficiently long password"); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("old password after reset = %v, want ErrBadCredentials", err)
	}
	spent := reset()
	if spent.Code != http.StatusUnprocessableEntity || len(spent.Result().Cookies()) != 0 {
		t.Errorf("spent reset = %d cookies=%v, want 422 without a prefill cookie", spent.Code, spent.Result().Cookies())
	}
}

func TestDeadResetLinksOfferRecovery(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, state := range []string{"expired", "spent"} {
			t.Run(locale.Tag()+"/"+state, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s := auth.NewStore(pool)
				u := registerProved(t, s, "reset-recovery-"+uuid.NewString()+"@goen.invalid")
				token := beginReset(t, s, u.Email)
				if state == "expired" {
					if _, err := pool.Exec(ctx, `UPDATE password_reset_tokens
					    SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 second'
					    WHERE digest = $1`, bearer.Digest(token)); err != nil {
						t.Fatal(err)
					}
				} else if _, err := s.CompleteReset(ctx, token, "the already completed password"); err != nil {
					t.Fatal(err)
				}
				h := auth.NewHandler(auth.NewStore(accountStorePool(t, "reset-recovery")), nil, nil, slog.New(slog.DiscardHandler), false, nil)
				before := accountRecoveryState(t, u.ID)
				page := httptest.NewRecorder()
				h.ResetPage(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/reset?token="+token, http.NoBody))
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="token" value="`+token+`"`) {
					t.Fatal("GET validated a dead token instead of presenting the reset form")
				}
				if after := accountRecoveryState(t, u.ID); after != before {
					t.Fatal("scanner GET changed account state")
				}
				res := httptest.NewRecorder()
				h.Reset(res, cartForm(ctx, "/reset", url.Values{
					"token": {token}, "password": {"the refused recovery password"}, "confirm": {"the refused recovery password"},
				}))
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("dead reset POST = %d, want 422", res.Code)
				}
				heading, reason := "This link is no longer valid", "That link has been used, has expired, or is not right."
				if locale == i18n.ZhHant {
					heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
					reason = "\u9019\u500b\u9023\u7d50\u5df2\u7d93\u7528\u904e\u3001\u904e\u671f\u6216\u4e0d\u6b63\u78ba\u3002"
				}
				uitest.AssertEmailLink(t, res.Body.String(), heading, reason, "/forgot")
				if strings.Contains(res.Body.String(), token) {
					t.Error("dead reset recovery leaks the token")
				}
				if after := accountRecoveryState(t, u.ID); after != before {
					t.Error("refused reset changed account state or password")
				}
			})
		}
	}
}
