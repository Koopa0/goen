//go:build integration

package account_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
)

func TestPasswordConfirmationDoesNotRecordASignIn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "password-confirmation"))
	u := registerProved(t, s, "confirm-password-"+uuid.NewString()+"@example.com")
	before := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	setLastLogin(t, u.ID, before)
	if err := s.ConfirmPassword(ctx, strings.ToUpper(u.Email), "a sufficiently long password"); err != nil {
		t.Fatalf("confirm the current password: %v", err)
	}
	wantLastLogin(t, u.ID, before, "ConfirmPassword")

	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	res := httptest.NewRecorder()
	h.SignIn(res, cartForm(ctx, "/signin", url.Values{
		"email": {u.Email}, "password": {"a sufficiently long password"}, "next": {"/account"},
	}))
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/account" {
		t.Fatalf("sign-in answered %d to %q, want 303 to /account", res.Code, res.Header().Get("Location"))
	}
	cookie := sessionCookie(t, res)
	if signedIn, err := s.SessionUser(ctx, cookie.Value); err != nil || signedIn.ID != u.ID {
		t.Fatalf("sign-in session belongs to %q with error %v, want %q", signedIn.ID, err, u.ID)
	}
	if got := lastLogin(t, u.ID); !got.After(before) {
		t.Errorf("sign-in last_login_at = %s, want after %s", got, before)
	}
}

func TestAccountPasswordConfirmationFormsDoNotRecordASignIn(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		values   url.Values
		location string
	}{
		{name: "password change", path: "/account/password", values: url.Values{
			"current": {"a sufficiently long password"}, "password": {"a different long password"}, "confirm": {"a different long password"},
		}, location: "/signin?changed=1"},
		{name: "email change", path: "/account/email", values: url.Values{
			"current": {"a sufficiently long password"}, "email": {"new-" + uuid.NewString() + "@example.com"},
		}, location: "/account?address=n%2A%2A%2A%40example.com&email=sent"},
		{name: "refused new password", path: "/account/password", values: url.Values{
			"current": {"a sufficiently long password"}, "password": {"short"}, "confirm": {"short"},
		}, location: "/account?password=invalid"},
		{name: "refused new email", path: "/account/email", values: url.Values{
			"current": {"a sufficiently long password"}, "email": {"not-an-email"},
		}, location: "/account?email=invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := account.NewStore(accountStorePool(t, "confirmation-form"))
			u := registerProved(t, s, "confirmation-form-"+uuid.NewString()+"@example.com")
			token, err := s.StartSession(ctx, u.ID, "confirmation test", "127.0.0.1")
			if err != nil {
				t.Fatalf("start the existing session: %v", err)
			}
			cookieResponse := httptest.NewRecorder()
			account.SetSessionCookie(cookieResponse, token, false)
			cookie := sessionCookie(t, cookieResponse)
			before := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
			setLastLogin(t, u.ID, before)
			h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /account/password", h.RequireUser(h.ChangePassword))
			mux.HandleFunc("POST /account/email", h.RequireUser(h.ChangeEmail))
			req := cartForm(ctx, tt.path, tt.values)
			req.AddCookie(cookie)
			res := httptest.NewRecorder()
			h.Authenticate(mux).ServeHTTP(res, req)
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != tt.location {
				t.Fatalf("%s answered %d to %q, want 303 to %q", tt.name, res.Code, res.Header().Get("Location"), tt.location)
			}
			wantLastLogin(t, u.ID, before, tt.name)
		})
	}
}

func TestConfirmPasswordKeepsTheCredentialRefusals(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "confirmation-refusal"))
	u := registerProved(t, s, "confirmation-refusal-"+uuid.NewString()+"@example.com")
	unproved := register(t, s, "confirmation-unproved-"+uuid.NewString()+"@example.com")
	withoutPassword := registerProved(t, s, "confirmation-no-password-"+uuid.NewString()+"@example.com")
	if _, err := pool.Exec(ctx, `UPDATE users SET password_hash = NULL WHERE id = $1`, uuid.MustParse(withoutPassword.ID)); err != nil {
		t.Fatalf("make the password-less account: %v", err)
	}
	before := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	for _, id := range []string{u.ID, unproved.ID, withoutPassword.ID} {
		setLastLogin(t, id, before)
	}
	tests := []struct {
		name     string
		email    string
		password string
	}{
		{name: "wrong password", email: u.Email, password: "not the password"},
		{name: "missing account", email: "missing-" + uuid.NewString() + "@example.com", password: "not the password"},
		{name: "unproved address", email: unproved.Email, password: "a sufficiently long password"},
		{name: "no password", email: withoutPassword.Email, password: "a sufficiently long password"},
		{name: "overlong password", email: u.Email, password: strings.Repeat("a", account.MaxPasswordBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.ConfirmPassword(t.Context(), tt.email, tt.password); !errors.Is(err, account.ErrBadCredentials) {
				t.Errorf("ConfirmPassword(%s) = %v, want ErrBadCredentials", tt.name, err)
			}
		})
	}
	for _, id := range []string{u.ID, unproved.ID, withoutPassword.ID} {
		wantLastLogin(t, id, before, "refused confirmation")
	}
}

func setLastLogin(t *testing.T, userID string, stamp time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE users SET last_login_at = $2 WHERE id = $1`, uuid.MustParse(userID), stamp); err != nil {
		t.Fatalf("set the previous sign-in: %v", err)
	}
}

func lastLogin(t *testing.T, userID string) time.Time {
	t.Helper()
	var stamp time.Time
	if err := pool.QueryRow(t.Context(), `SELECT last_login_at FROM users WHERE id = $1`, uuid.MustParse(userID)).Scan(&stamp); err != nil {
		t.Fatalf("read the last sign-in: %v", err)
	}
	return stamp
}

func wantLastLogin(t *testing.T, userID string, want time.Time, action string) {
	t.Helper()
	if got := lastLogin(t, userID); !got.Equal(want) {
		t.Errorf("%s recorded a sign-in: last_login_at = %s, want %s", action, got, want)
	}
}
