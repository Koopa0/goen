//go:build integration

package auth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/bearer"
)

func TestSessionsAreStoredHashed(t *testing.T) {
	s := auth.NewStore(pool)
	u := register(t, s, "session@example.com")

	token, err := s.StartSession(t.Context(), u.ID, "test-agent", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var n int
	if countErr := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE digest = $1`, []byte(token)).Scan(&n); countErr != nil {
		t.Fatalf("count: %v", countErr)
	}
	if n != 0 {
		t.Error("the session table is keyed by the raw token; a leak would hand over live sessions")
	}

	got, err := s.SessionUser(t.Context(), token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser(token) = %v/%v, want %s", got.ID, err, u.ID)
	}
	if _, err := s.SessionUser(t.Context(), token+"x"); !errors.Is(err, auth.ErrNotFound) {
		t.Error("a near-miss token found a session")
	}
}

func TestUnsafeUserAgentDoesNotRefuseOrDecorateASession(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "session-user-agent-"+uuid.NewString()+"@example.com")

	for _, tt := range []struct {
		name      string
		userAgent string
	}{
		{name: "overlong", userAgent: strings.Repeat("a", 513)},
		{name: "control character", userAgent: "browser\nforged"},
		{name: "not UTF-8", userAgent: "Mozilla/5.0 Caf\xe9Browser/1.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token, err := s.StartSession(ctx, u.ID, tt.userAgent, "192.0.2.1")
			if err != nil {
				t.Fatalf("unsafe optional user-agent refused the session: %v", err)
			}
			if got, sessionErr := s.SessionUser(ctx, token); sessionErr != nil || got.ID != u.ID {
				t.Fatalf("session created without unsafe decoration resolved to %q/%v, want %s/nil",
					got.ID, sessionErr, u.ID)
			}

			var stored *string
			if err := pool.QueryRow(ctx,
				`SELECT user_agent FROM sessions WHERE digest = $1`, bearer.Digest(token)).
				Scan(&stored); err != nil {
				t.Fatalf("read persisted user-agent: %v", err)
			}
			if stored != nil {
				t.Errorf("unsafe user-agent persisted as %q, want NULL decoration", *stored)
			}
		})
	}

	safe := strings.Repeat("a", 512)
	token, err := s.StartSession(ctx, u.ID, safe, "192.0.2.1")
	if err != nil {
		t.Fatalf("exact safe user-agent ceiling refused the session: %v", err)
	}
	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT user_agent FROM sessions WHERE digest = $1`, bearer.Digest(token)).
		Scan(&stored); err != nil {
		t.Fatalf("read exact-ceiling user-agent: %v", err)
	}
	if stored != safe {
		t.Errorf("exact-ceiling user-agent persisted as %d runes, want %d",
			len([]rune(stored)), len([]rune(safe)))
	}
}

func TestExpiredSessionIsNobody(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "expired@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	// Both timestamps move: sessions_expiry_after_creation refuses a row whose
	// window closed before it opened.
	if _, err = pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE digest = $1`, bearer.Digest(token)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE digest = $1`,
		bearer.Digest(token)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("the session row is gone; this test would prove nothing")
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, auth.ErrNotFound) {
		t.Error("an expired session still resolved to a user")
	}
}

func TestSessionExpiryUsesTheDatabaseClockAndTTL(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "ttl@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	var expires time.Time
	var exactTTL bool
	if err := pool.QueryRow(ctx, `
		SELECT expires_at,
		       expires_at = created_at + ($2 * interval '1 second')
		FROM sessions WHERE digest = $1`,
		bearer.Digest(token), auth.SessionTTL).Scan(&expires, &exactTTL); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !exactTTL {
		t.Error("session expiry was not derived from the row's database creation clock and TTL")
	}
	if !expires.After(time.Now().Add(24 * time.Hour)) {
		t.Errorf("session expires at %v, which is less than a day away", expires)
	}
}

type statusRecorder struct {
	*httptest.ResponseRecorder

	statuses []int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	w.ResponseRecorder.WriteHeader(status)
}

// TestSessionWriteFailureDoesNotFallThroughToRedirect proves all three entry
// paths stop after startSession writes its 500. net/http keeps the first status
// code, so a second WriteHeader is what this observes.
func TestSessionWriteFailureDoesNotFallThroughToRedirect(t *testing.T) {
	s := auth.NewStore(pool)
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	existing := registerProved(t, s, "session-failure-signin-"+uuid.NewString()+"@example.com")

	for _, tt := range []struct {
		name   string
		target string
		form   func(t *testing.T) url.Values
		handle http.HandlerFunc
	}{
		{
			name: "sign in", target: "/signin", handle: h.SignIn,
			form: func(*testing.T) url.Values {
				return url.Values{
					"email": {existing.Email}, "password": {"a sufficiently long password"},
					"next": {"/account"},
				}
			},
		},
		{
			// A registration starts its session when its link is followed with
			// the password chosen at registration.
			name: "registration link", target: "/register/complete", handle: h.CompleteRegistration,
			form: func(t *testing.T) url.Values {
				t.Helper()
				pending := register(t, s, "session-failure-register-"+uuid.NewString()+"@example.com")
				return url.Values{
					"token":    {requestVerification(t, s, pending.ID, pending.Email)},
					"password": {"a sufficiently long password"},
					"next":     {"/account"},
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			forceSessionInsertFailure(t)
			body := tt.form(t).Encode()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				tt.target, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			out := &statusRecorder{ResponseRecorder: httptest.NewRecorder()}
			tt.handle(out, req)
			if out.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", out.Code)
			}
			if len(out.statuses) != 1 || out.statuses[0] != http.StatusInternalServerError {
				t.Errorf("WriteHeader calls = %v, want one 500 and no redirect", out.statuses)
			}
			if location := out.Header().Get("Location"); location != "" {
				t.Errorf("session failure also wrote redirect Location %q", location)
			}
		})
	}

	googleSignIn, err := google.New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("new google client: %v", err)
	}
	google.SetHTTPClient(googleSignIn, &http.Client{Transport: roundTripFunc(
		func(r *http.Request) (*http.Response, error) {
			var body string
			switch r.URL.String() {
			case "https://oauth2.googleapis.com/token":
				body = `{"access_token":"test-access-token"}`
			case "https://openidconnect.googleapis.com/v1/userinfo":
				body = fmt.Sprintf(
					`{"sub":%q,"email":%q,"email_verified":true,"name":"Google Test"}`,
					"session-failure-google-"+uuid.NewString(),
					"session-failure-google-"+uuid.NewString()+"@example.com",
				)
			default:
				return nil, fmt.Errorf("unexpected google endpoint %s", r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		},
	)})
	googleHandler := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, googleSignIn)

	start := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google?next=/account", http.NoBody)
	startOut := httptest.NewRecorder()
	googleHandler.GoogleSignIn(startOut, start)
	if startOut.Code != http.StatusSeeOther {
		t.Fatalf("start google sign-in status = %d, want 303", startOut.Code)
	}
	target, err := url.Parse(startOut.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse google redirect: %v", err)
	}
	state := target.Query().Get("state")
	if state == "" {
		t.Fatal("google redirect has no state")
	}
	cookies := startOut.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("google start cookies = %d, want OAuth state cookie", len(cookies))
	}

	forceSessionInsertFailure(t)
	callback := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(state)+"&code=test-code", http.NoBody)
	callback.AddCookie(cookies[0])
	callbackOut := &statusRecorder{ResponseRecorder: httptest.NewRecorder()}
	googleHandler.GoogleCallback(callbackOut, callback)
	if callbackOut.Code != http.StatusInternalServerError {
		t.Fatalf("google callback status = %d, want 500", callbackOut.Code)
	}
	if len(callbackOut.statuses) != 1 || callbackOut.statuses[0] != http.StatusInternalServerError {
		t.Errorf("google callback WriteHeader calls = %v, want one 500 and no redirect",
			callbackOut.statuses)
	}
	if location := callbackOut.Header().Get("Location"); location != "" {
		t.Errorf("google session failure also wrote redirect Location %q", location)
	}
}

func forceSessionInsertFailure(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_session_insert_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_session_insert_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			RAISE EXCEPTION USING MESSAGE = 'forced session insert failure',
			                      ERRCODE = 'check_violation';
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON sessions
		FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install session insert failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON sessions; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})
}

func TestExpiredSessionsArePruned(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	u := register(t, s, "pruned@example.com")

	live, err := s.StartSession(ctx, u.ID, "live", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	dead, err := s.StartSession(ctx, u.ID, "dead", "")
	if err != nil {
		t.Fatalf("start second session: %v", err)
	}
	// created_at moves with it: sessions_expiry_after_creation refuses a row
	// whose window closed before it opened.
	tag, err := pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 second'
		WHERE digest = sha256($1::bytea)`, dead)
	if err != nil {
		t.Fatalf("age the session: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("aged %d rows, want 1 — the session is not where this test looks",
			tag.RowsAffected())
	}

	if err := s.SweepSessions(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if n := sessionRows(t, u.ID); n != 1 {
		t.Errorf("%d session rows survive, want 1 — the expired one was not deleted", n)
	}
	// A pruner that deleted everything would pass a count-only assertion.
	if _, err := s.SessionUser(ctx, live); err != nil {
		t.Errorf("the live session was pruned too: %v", err)
	}
}

func sessionRows(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}
