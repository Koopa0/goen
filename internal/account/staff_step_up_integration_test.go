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

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
)

// A second-factor check that cannot be answered refuses: the four doors that
// end, replace or re-credential a staff account must not read "I could not tell"
// as "verified". The cmd/goen tests drive the real predicate; this one drives a
// predicate that fails, which the real one only does when the database does.
func TestStaffDoorsRefuseWhenTheSecondFactorCannotBeRead(t *testing.T) {
	failing := func(*http.Request) (bool, error) { return false, errors.New("sessions unreachable") }

	for _, door := range []struct {
		path string
		call func(*account.Handler) http.HandlerFunc
	}{
		{"/account/erase", func(h *account.Handler) http.HandlerFunc { return h.Erase }},
		{"/account/password", func(h *account.Handler) http.HandlerFunc { return h.ChangePassword }},
		{"/account/sessions/others/end", func(h *account.Handler) http.HandlerFunc { return h.SignOutOtherDevices }},
		{"/account/google/unlink", func(h *account.Handler) http.HandlerFunc { return h.UnlinkGoogle }},
	} {
		t.Run(door.path, func(t *testing.T) {
			s := account.NewStore(accountStorePool(t, "staff-step-up"))
			u := registerProved(t, s, "step-up-"+uuid.NewString()+"@example.com")
			if _, err := pool.Exec(t.Context(), `UPDATE users SET role = 'staff' WHERE id = $1`, uuid.MustParse(u.ID)); err != nil {
				t.Fatalf("make staff: %v", err)
			}
			token, err := s.StartSession(t.Context(), u.ID, "step-up", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartSession(t.Context(), u.ID, "other", "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
			h.RequireStepUpFromStaff(failing)

			rec := httptest.NewRecorder()
			account.SetSessionCookie(rec, token, false)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, door.path, strings.NewReader(""))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(sessionCookie(t, rec))
			res := httptest.NewRecorder()
			h.Authenticate(door.call(h)).ServeHTTP(res, req)

			if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
				t.Errorf("%s = %d %q, want 500 and no redirect", door.path, res.Code, res.Header().Get("Location"))
			}
			var accounts, sessions int
			if err := pool.QueryRow(t.Context(), `
				SELECT (SELECT count(*) FROM users WHERE id = $1),
				       (SELECT count(*) FROM sessions WHERE user_id = $1)`, uuid.MustParse(u.ID)).Scan(&accounts, &sessions); err != nil {
				t.Fatal(err)
			}
			if accounts != 1 || sessions != 2 {
				t.Errorf("accounts=%d sessions=%d after the refusal, want 1 and 2", accounts, sessions)
			}
		})
	}
}

// The profile and the other self-service edits stay open to a staff account that
// has not passed the second factor: they neither end an account, nor change how
// it signs in, nor reach anything the back office guards.
func TestStaffProfileEditNeedsNoSecondFactor(t *testing.T) {
	s := account.NewStore(accountStorePool(t, "staff-profile"))
	u := registerProved(t, s, "step-up-profile-"+uuid.NewString()+"@example.com")
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role = 'staff' WHERE id = $1`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("make staff: %v", err)
	}
	token, err := s.StartSession(t.Context(), u.ID, "step-up", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	h.RequireStepUpFromStaff(func(*http.Request) (bool, error) { return false, nil })

	rec := httptest.NewRecorder()
	account.SetSessionCookie(rec, token, false)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/profile",
		strings.NewReader(url.Values{"name": {"Renamed"}, "phone": {""}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sessionCookie(t, rec))
	res := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.UpdateProfile)).ServeHTTP(res, req)

	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/account?saved=1" {
		t.Errorf("profile edit = %d %q, want 303 /account?saved=1", res.Code, res.Header().Get("Location"))
	}
}
