//go:build integration

package auth_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
)

const wrongPassword = "not the password at all"

// signInFrom posts one sign-in as a client at remoteAddr. The per-client limit
// that fronts /signin in the server is not in this handler, so only the budgets
// the handler owns are in play.
func signInFrom(t *testing.T, h *auth.Handler, remoteAddr, addr, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := cartForm(t.Context(), "/signin", url.Values{
		"email": {addr}, "password": {password}, "next": {"/account"},
	})
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	h.SignIn(rec, req)
	return rec
}

// network is the n-th client network: a /24 of its own, so no two share a
// per-network budget.
func network(n int) string { return fmt.Sprintf("198.51.%d.9:4000", n) }

func newGuessHandler(t *testing.T) (*auth.Handler, *auth.Store) {
	t.Helper()
	s := auth.NewStore(pool)
	return auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil), s
}

// TestHandler_SignInStrangerMisses: six wrong passwords from one client hold
// that client's network (and its /24) at 429 but do not spend the owner's
// sign-in: the right password from another client still gets in. This is the
// audit's reproduction (finding A1): the account's budget used to be charged by
// the attempt, so the owner got 429.
func TestHandler_SignInStrangerMisses(t *testing.T) {
	h, s := newGuessHandler(t)
	owner := registerProved(t, s, "owner-"+uuid.NewString()+"@example.com")

	const attacker = "203.0.113.9:5000"
	for i := range 6 {
		if got := signInFrom(t, h, attacker, owner.Email, wrongPassword).Code; got != http.StatusUnprocessableEntity {
			t.Fatalf("wrong password %d from the attacker answered %d, want 422", i+1, got)
		}
	}
	// The attacker's own network is held at its budget, and so is a neighbour in
	// the same /24: the throttle is per network, not per address.
	for _, from := range []string{attacker, "203.0.113.200:5000"} {
		if got := signInFrom(t, h, from, owner.Email, wrongPassword).Code; got != http.StatusTooManyRequests {
			t.Errorf("a seventh wrong password from %s answered %d, want 429: the attacker's network was not held", from, got)
		}
	}

	if got := signInFrom(t, h, "192.0.2.77:6000", owner.Email, cartOwnerPassword).Code; got != http.StatusSeeOther {
		t.Errorf("the owner's right password from another client answered %d after six wrong ones from a stranger, "+
			"want 303: the stranger spent the owner's allowance", got)
	}
}

// TestHandler_SignInSpreadGuess: a guess spread over many networks meets the
// per-account ceiling (429 even for the right password, since the ceiling is
// reserved before the hash), while the owner who arrives before it is reached
// gets in and right passwords are not charged. Each network is within its own
// budget, so only the ceiling over the account can stop this.
func TestHandler_SignInSpreadGuess(t *testing.T) {
	t.Run("the owner within the ceiling gets in", func(t *testing.T) {
		h, s := newGuessHandler(t)
		owner := registerProved(t, s, "ceiling-in-"+uuid.NewString()+"@example.com")
		for n := range 9 {
			if got := signInFrom(t, h, network(n), owner.Email, wrongPassword).Code; got != http.StatusUnprocessableEntity {
				t.Fatalf("wrong password %d from network %d answered %d, want 422", n+1, n, got)
			}
		}
		if got := signInFrom(t, h, network(100), owner.Email, cartOwnerPassword).Code; got != http.StatusSeeOther {
			t.Errorf("the right password after nine failures from nine networks answered %d, want 303", got)
		}
	})

	t.Run("the ceiling holds when it is reached", func(t *testing.T) {
		h, s := newGuessHandler(t)
		owner := registerProved(t, s, "ceiling-out-"+uuid.NewString()+"@example.com")
		for n := range 10 {
			if got := signInFrom(t, h, network(n), owner.Email, wrongPassword).Code; got != http.StatusUnprocessableEntity {
				t.Fatalf("wrong password %d from network %d answered %d, want 422", n+1, n, got)
			}
		}
		refused := signInFrom(t, h, network(11), owner.Email, wrongPassword)
		if refused.Code != http.StatusTooManyRequests {
			t.Fatalf("the eleventh wrong password, from a network that had sent none, answered %d, want 429: "+
				"a guess spread over networks is not bounded", refused.Code)
		}
		// Reserved before the hash, so the refusal cannot say whether the
		// password was right: the right one is refused too.
		if got := signInFrom(t, h, network(12), owner.Email, cartOwnerPassword).Code; got != http.StatusTooManyRequests {
			t.Errorf("the right password at the ceiling answered %d, want 429: the refusal came after the check", got)
		}
		if wait, err := strconv.Atoi(refused.Header().Get("Retry-After")); err != nil || wait < 1 || wait > 90 {
			t.Errorf("Retry-After = %q, want a wait of at most one refill (90 s)", refused.Header().Get("Retry-After"))
		}
	})

	t.Run("a right password is not charged", func(t *testing.T) {
		h, s := newGuessHandler(t)
		owner := registerProved(t, s, "ceiling-free-"+uuid.NewString()+"@example.com")
		for n := range 25 {
			if got := signInFrom(t, h, network(n), owner.Email, cartOwnerPassword).Code; got != http.StatusSeeOther {
				t.Fatalf("sign-in %d from network %d answered %d, want 303: right passwords are spending the ceiling", n+1, n, got)
			}
		}
	})
}

// TestHandler_CompleteRegistrationMisses: completing a registration shares the
// sign-in budgets: a stranger's wrong passwords hold their own network at 429
// but do not refuse the registrant who types the password they chose from
// another client.
func TestHandler_CompleteRegistrationMisses(t *testing.T) {
	h, s := newGuessHandler(t)
	addr := "registrant-" + uuid.NewString() + "@example.com"
	if err := s.Register(t.Context(), &auth.Credentials{
		Email: addr, Password: cartOwnerPassword, Name: "註冊測試",
	}, "/account"); err != nil {
		t.Fatalf("register %s: %v", addr, err)
	}
	if n := followUpRegistrations(t, s, addr, neverTold(t)); n != 1 {
		t.Fatalf("%d registrations were queued for %s, want 1", n, addr)
	}
	token, next := queuedLink(t, addr)
	complete := func(remoteAddr, password string) int {
		req := cartForm(t.Context(), "/register/complete", url.Values{
			"token": {token}, "next": {next}, "password": {password},
		})
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		h.CompleteRegistration(rec, req)
		return rec.Code
	}

	for i := range 6 {
		if got := complete("203.0.113.9:5000", wrongPassword); got != http.StatusUnprocessableEntity {
			t.Fatalf("wrong password %d answered %d, want 422", i+1, got)
		}
	}
	if got := complete("203.0.113.9:5000", wrongPassword); got != http.StatusTooManyRequests {
		t.Errorf("a seventh wrong password from one network answered %d, want 429", got)
	}
	if got := complete("192.0.2.77:6000", cartOwnerPassword); got != http.StatusSeeOther {
		t.Errorf("the registrant's right password from another client answered %d, want 303", got)
	}
}
