//go:build integration

package auth_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/db/dbtest"
)

// The handler and store below run as store, the role the storefront pool logs
// in as, so a grant known_devices lacks fails here as it would in production.
func newDeviceHandler(t *testing.T) (*auth.Handler, *auth.Store) {
	t.Helper()
	s := auth.NewStore(dbtest.RolePool(t, pool, "store"))
	return auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil), s
}

// signInWith posts one sign-in from remoteAddr, presenting device when it is
// not empty, and returns the response.
func signInWith(t *testing.T, h *auth.Handler, remoteAddr, device, addr, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := cartForm(t.Context(), "/signin", url.Values{
		"email": {addr}, "password": {password}, "next": {"/account"},
	})
	req.RemoteAddr = remoteAddr
	if device != "" {
		req.AddCookie(&http.Cookie{Name: "goen_device", Value: device}) //nolint:gosec // G124: the development cookie a browser sends back
	}
	rec := httptest.NewRecorder()
	h.SignIn(rec, req)
	return rec
}

// deviceFrom is the device token rec handed the browser, or "" when it set none.
func deviceFrom(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goen_device" && c.MaxAge > 0 {
			return c.Value
		}
	}
	return ""
}

// signedInDevice signs the owner in from a browser of its own and returns the
// device token that browser now holds.
func signedInDevice(t *testing.T, h *auth.Handler, addr string) string {
	t.Helper()
	rec := signInWith(t, h, network(200), "", addr, cartOwnerPassword)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the owner's first sign-in answered %d, want 303", rec.Code)
	}
	device := deviceFrom(rec)
	if device == "" {
		t.Fatal("a successful sign-in set no device cookie")
	}
	return device
}

// emptyTheCeiling is the #1953 attack: ten wrong passwords, one from each of ten
// networks, so no per-network budget fires and the account's ceiling is spent.
func emptyTheCeiling(t *testing.T, h *auth.Handler, addr string) {
	t.Helper()
	for n := range 10 {
		if got := signInWith(t, h, network(n), "", addr, wrongPassword).Code; got != http.StatusUnprocessableEntity {
			t.Fatalf("stranger's wrong password %d answered %d, want 422", n+1, got)
		}
	}
	if got := signInWith(t, h, network(11), "", addr, wrongPassword).Code; got != http.StatusTooManyRequests {
		t.Fatalf("the stranger's eleventh wrong password answered %d, want 429: the ceiling did not hold", got)
	}
}

func deviceRows(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM known_devices WHERE user_id = $1`,
		uuid.MustParse(userID)).Scan(&n); err != nil {
		t.Fatalf("count known devices: %v", err)
	}
	return n
}

// TestHandler_SignInKnownDevice: the owner's remembered browser signs in with
// the right password while a stranger holds the account's failure ceiling
// empty, and a browser the account has never seen is still refused (429). This
// is #1953's residual: a stranger who keeps the ceiling empty refused the owner
// too.
func TestHandler_SignInKnownDevice(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "known-device-"+uuid.NewString()+"@example.com")
	device := signedInDevice(t, h, owner.Email)

	emptyTheCeiling(t, h, owner.Email)

	if got := signInWith(t, h, network(101), device, owner.Email, cartOwnerPassword).Code; got != http.StatusSeeOther {
		t.Errorf("the owner's browser with the right password answered %d while a stranger held the ceiling, want 303", got)
	}
	if got := signInWith(t, h, network(102), "", owner.Email, cartOwnerPassword).Code; got != http.StatusTooManyRequests {
		t.Errorf("a browser the account has never seen answered %d at an empty ceiling, want 429", got)
	}
}

// TestHandler_SignInDeviceOtherAccount: a device cookie remembered for one
// account spends nobody else's budget: presented with another account's address
// at that account's empty ceiling it is a stranger's guess and is refused
// (429).
func TestHandler_SignInDeviceOtherAccount(t *testing.T) {
	h, s := newDeviceHandler(t)
	x := registerProved(t, s, "device-x-"+uuid.NewString()+"@example.com")
	y := registerProved(t, s, "device-y-"+uuid.NewString()+"@example.com")
	device := signedInDevice(t, h, x.Email)

	emptyTheCeiling(t, h, y.Email)

	if got := signInWith(t, h, network(101), device, y.Email, cartOwnerPassword).Code; got != http.StatusTooManyRequests {
		t.Errorf("X's browser signing in to Y at Y's empty ceiling answered %d, want 429", got)
	}
}

// TestHandler_SignInDeviceMisses: once DeviceMisses wrong passwords are
// recorded against a device cookie the account forgets that browser, so a
// stolen cookie is a stranger's browser again. The cookie authenticates
// nothing, and what it buys is counted in the database, so a restart does not
// refill it.
func TestHandler_SignInDeviceMisses(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "stolen-device-"+uuid.NewString()+"@example.com")
	device := signedInDevice(t, h, owner.Email)

	// The device's own budget is a network's: six at once. A new handler is a
	// restart, which refills every budget the process holds.
	sent := 0
	for sent < auth.DeviceMisses {
		h, _ = newDeviceHandler(t)
		for range 6 {
			if sent == auth.DeviceMisses {
				break
			}
			if got := signInWith(t, h, network(150+sent), device, owner.Email, wrongPassword).Code; got != http.StatusUnprocessableEntity {
				t.Fatalf("wrong password %d with the cookie answered %d, want 422", sent+1, got)
			}
			sent++
		}
	}
	if n := deviceRows(t, owner.ID); n != 0 {
		t.Errorf("the account still remembers %d browsers after %d wrong passwords from one, want 0", n, auth.DeviceMisses)
	}

	h, _ = newDeviceHandler(t)
	emptyTheCeiling(t, h, owner.Email)
	if got := signInWith(t, h, network(101), device, owner.Email, cartOwnerPassword).Code; got != http.StatusTooManyRequests {
		t.Errorf("the spent cookie with the right password answered %d at an empty ceiling, want 429", got)
	}
}

// TestHandler_SignInDeviceRotates: each sign-in replaces the browser's device
// row and cookie, so a copy of an older cookie stops counting once its browser
// signs in again.
func TestHandler_SignInDeviceRotates(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "rotate-device-"+uuid.NewString()+"@example.com")
	first := signedInDevice(t, h, owner.Email)

	rec := signInWith(t, h, network(201), first, owner.Email, cartOwnerPassword)
	second := deviceFrom(rec)
	if rec.Code != http.StatusSeeOther || second == "" || second == first {
		t.Fatalf("signing in with a remembered browser answered %d and handed it %q, want 303 and a new token", rec.Code, second)
	}
	if n := deviceRows(t, owner.ID); n != 1 {
		t.Errorf("one browser signing in twice left %d rows, want 1", n)
	}

	emptyTheCeiling(t, h, owner.Email)
	if got := signInWith(t, h, network(101), first, owner.Email, cartOwnerPassword).Code; got != http.StatusTooManyRequests {
		t.Errorf("the replaced cookie answered %d at an empty ceiling, want 429", got)
	}
	if got := signInWith(t, h, network(102), second, owner.Email, cartOwnerPassword).Code; got != http.StatusSeeOther {
		t.Errorf("the current cookie answered %d at an empty ceiling, want 303", got)
	}
}

// TestHandler_SignInDeviceLimit: an account remembers at most DevicesPerAccount
// browsers: the oldest goes when one more signs in.
func TestHandler_SignInDeviceLimit(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "many-devices-"+uuid.NewString()+"@example.com")
	oldest := signedInDevice(t, h, owner.Email)
	for range auth.DevicesPerAccount {
		signedInDevice(t, h, owner.Email)
	}
	if n := deviceRows(t, owner.ID); n != auth.DevicesPerAccount {
		t.Errorf("after %d browsers signed in the account remembers %d, want %d",
			auth.DevicesPerAccount+1, n, auth.DevicesPerAccount)
	}
	var kept bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM known_devices WHERE digest = $1)`,
		bearer.Digest(oldest)).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept {
		t.Error("the first browser is still remembered; the limit dropped a newer one")
	}
}

// TestHandler_ResetDevice: a browser the account has never seen gets past a
// held ceiling through a password reset by mail, and the reset forgets every
// browser the old password vouched for.
func TestHandler_ResetDevice(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "reset-device-"+uuid.NewString()+"@example.com")
	before := signedInDevice(t, h, owner.Email)
	emptyTheCeiling(t, h, owner.Email)

	const newPassword = "the password chosen after the reset"
	req := cartForm(t.Context(), "/reset", url.Values{
		"token": {beginReset(t, s, owner.Email)}, "password": {newPassword}, "confirm": {newPassword},
	})
	rec := httptest.NewRecorder()
	h.Reset(rec, req)
	after := deviceFrom(rec)
	if rec.Code != http.StatusSeeOther || after == "" {
		t.Fatalf("the reset answered %d and handed the browser %q, want 303 and a device cookie", rec.Code, after)
	}
	if n := deviceRows(t, owner.ID); n != 1 {
		t.Errorf("after the reset the account remembers %d browsers, want only the one that reset it", n)
	}

	if got := signInWith(t, h, network(102), before, owner.Email, newPassword).Code; got != http.StatusTooManyRequests {
		t.Errorf("a browser remembered before the reset answered %d at an empty ceiling, want 429", got)
	}
	if got := signInWith(t, h, network(101), after, owner.Email, newPassword).Code; got != http.StatusSeeOther {
		t.Errorf("the browser that completed the reset answered %d at an empty ceiling, want 303", got)
	}
}

// TestHandler_RevokeForgetsDevices: a password change, a sign-out of other
// devices and erasure each forget the account's remembered browsers (a sign-out
// of other devices keeps this one), and signing out of one browser keeps it.
func TestHandler_RevokeForgetsDevices(t *testing.T) {
	t.Run("signing out keeps the browser", func(t *testing.T) {
		h, s := newDeviceHandler(t)
		owner := registerProved(t, s, "signout-device-"+uuid.NewString()+"@example.com")
		rec := signInWith(t, h, network(200), "", owner.Email, cartOwnerPassword)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signout", nil)
		req.AddCookie(sessionCookie(t, rec))
		h.SignOut(httptest.NewRecorder(), req)
		if n := deviceRows(t, owner.ID); n != 1 {
			t.Errorf("signing out left %d remembered browsers, want 1", n)
		}
	})

	t.Run("a password change forgets every browser", func(t *testing.T) {
		h, s := newDeviceHandler(t)
		owner := registerProved(t, s, "change-device-"+uuid.NewString()+"@example.com")
		signedInDevice(t, h, owner.Email)
		signedInDevice(t, h, owner.Email)
		if err := s.ChangePassword(t.Context(), owner.ID, "a changed and long password"); err != nil {
			t.Fatal(err)
		}
		if n := deviceRows(t, owner.ID); n != 0 {
			t.Errorf("a password change left %d remembered browsers, want 0", n)
		}
	})

	t.Run("signing out other devices keeps only this browser", func(t *testing.T) {
		h, s := newDeviceHandler(t)
		owner := registerProved(t, s, "others-device-"+uuid.NewString()+"@example.com")
		other := registerProved(t, s, "others-bystander-"+uuid.NewString()+"@example.com")
		signedInDevice(t, h, owner.Email)
		signedInDevice(t, h, other.Email)
		rec := signInWith(t, h, network(201), "", owner.Email, cartOwnerPassword)
		this := deviceFrom(rec)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/sessions/others/end", nil)
		req.AddCookie(sessionCookie(t, rec))
		req.AddCookie(&http.Cookie{Name: "goen_device", Value: this}) //nolint:gosec // G124: the development cookie a browser sends back
		out := httptest.NewRecorder()
		h.Authenticate(http.HandlerFunc(h.SignOutOtherDevices)).ServeHTTP(out, req)
		if out.Code != http.StatusSeeOther {
			t.Fatalf("signing out other devices answered %d, want 303", out.Code)
		}
		var mine, theirs int
		if err := pool.QueryRow(t.Context(), `
			SELECT count(*) FILTER (WHERE digest = $1), count(*) FROM known_devices WHERE user_id = $2`,
			bearer.Digest(this), uuid.MustParse(owner.ID)).Scan(&mine, &theirs); err != nil {
			t.Fatal(err)
		}
		if mine != 1 || theirs != 1 {
			t.Errorf("the account remembers %d browsers, %d of them this one; want only this one", theirs, mine)
		}
		if n := deviceRows(t, other.ID); n != 1 {
			t.Errorf("another account's browser was forgotten: %d left, want 1", n)
		}
	})

	t.Run("erasure takes the browsers with the account", func(t *testing.T) {
		h, s := newDeviceHandler(t)
		owner := registerProved(t, s, "erase-device-"+uuid.NewString()+"@example.com")
		signedInDevice(t, h, owner.Email)
		if err := s.Erase(t.Context(), owner.ID); err != nil {
			t.Fatal(err)
		}
		if n := deviceRows(t, owner.ID); n != 0 {
			t.Errorf("erasure left %d remembered browsers, want 0", n)
		}
	})
}

// TestStore_SweepSessionsDevices: SweepSessions deletes a remembered browser
// that has not signed in for DeviceTTL and keeps a live one, and a remembered
// browser lasts DeviceTTL after its sign-in, the period the privacy policy
// states.
func TestStore_SweepSessionsDevices(t *testing.T) {
	h, s := newDeviceHandler(t)
	owner := registerProved(t, s, "sweep-device-"+uuid.NewString()+"@example.com")
	lapsed := signedInDevice(t, h, owner.Email)
	live := signedInDevice(t, h, owner.Email)
	if _, err := pool.Exec(t.Context(), `
		UPDATE known_devices SET created_at = now() - $2::interval - interval '1 day',
		                         expires_at = now() - interval '1 second'
		WHERE digest = $1`, bearer.Digest(lapsed), auth.DeviceTTL.String()); err != nil {
		t.Fatal(err)
	}
	if err := s.SweepSessions(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	for _, tc := range []struct {
		token string
		want  bool
	}{{lapsed, false}, {live, true}} {
		var kept bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM known_devices WHERE digest = $1)`,
			bearer.Digest(tc.token)).Scan(&kept); err != nil {
			t.Fatal(err)
		}
		if kept != tc.want {
			t.Errorf("after the sweep the browser is remembered = %t, want %t", kept, tc.want)
		}
	}
	// The period the privacy policy states: expires_at is DeviceTTL after the
	// sign-in that wrote the row.
	var lifetime float64
	if err := pool.QueryRow(t.Context(), `SELECT extract(epoch FROM expires_at - created_at) FROM known_devices WHERE digest = $1`,
		bearer.Digest(live)).Scan(&lifetime); err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(lifetime) * time.Second; got != auth.DeviceTTL {
		t.Errorf("a remembered browser lasts %v after its sign-in, want %v", got, auth.DeviceTTL)
	}
}
