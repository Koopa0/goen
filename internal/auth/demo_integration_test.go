//go:build integration

package auth_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

const sharedPassword = "a sufficiently long password"

func demoAccount(t *testing.T, addr, password string) auth.DemoAccount {
	t.Helper()
	d, err := auth.NewDemoAccount(addr, password)
	if err != nil {
		t.Fatalf("NewDemoAccount: %v", err)
	}
	return d
}

func passwordHashOf(t *testing.T, addr string) string {
	t.Helper()
	var hash string
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(password_hash, '') FROM users WHERE lower(email) = lower($1)`, addr).Scan(&hash); err != nil {
		t.Fatalf("read the password hash of %s: %v", addr, err)
	}
	return hash
}

// TestStore_EnsureDemoAccount: EnsureDemoAccount creates the demo account at
// the first start, leaves a matching password and a visitor's session alone at
// the next, and restores the configured password, ending sessions, when someone
// changed it. It runs on the storefront role, the one goen starts with: no
// grant or definer function exists for the demo account.
func TestStore_EnsureDemoAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "demo-ensure"))
	addr := "demo-" + uuid.NewString() + "@example.com"
	d := demoAccount(t, strings.ToUpper(addr), sharedPassword)

	if err := s.EnsureDemoAccount(ctx, d); err != nil {
		t.Fatalf("first start: %v", err)
	}
	u, signInErr := s.Authenticate(ctx, addr, sharedPassword)
	if signInErr != nil {
		t.Fatalf("the demo password does not sign in after the first start: %v", signInErr)
	}
	if u.Role != "customer" {
		t.Errorf("the demo account's role is %q, want customer", u.Role)
	}
	hash := passwordHashOf(t, addr)
	session, sessionErr := s.StartSession(ctx, u.ID, "a visitor", "")
	if sessionErr != nil {
		t.Fatalf("start a session: %v", sessionErr)
	}

	if err := s.EnsureDemoAccount(ctx, d); err != nil {
		t.Fatalf("second start: %v", err)
	}
	var holders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, addr).Scan(&holders); err != nil {
		t.Fatalf("count the accounts: %v", err)
	}
	if holders != 1 {
		t.Errorf("%d accounts hold the demo address after two starts, want 1", holders)
	}
	if passwordHashOf(t, addr) != hash {
		t.Error("a start whose password already matched rewrote it")
	}
	if _, err := s.SessionUser(ctx, session); err != nil {
		t.Errorf("a start whose password already matched ended a visitor's session: %v", err)
	}

	other, hashErr := auth.HashPassword("a password somebody else set")
	if hashErr != nil {
		t.Fatalf("hash: %v", hashErr)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, uuid.MustParse(u.ID), other); err != nil {
		t.Fatalf("change the stored password: %v", err)
	}
	if err := s.EnsureDemoAccount(ctx, d); err != nil {
		t.Fatalf("start over a changed password: %v", err)
	}
	if _, err := s.Authenticate(ctx, addr, sharedPassword); err != nil {
		t.Errorf("the configured password was not restored: %v", err)
	}
	if _, err := s.Authenticate(ctx, addr, "a password somebody else set"); err == nil {
		t.Error("the changed password still signs in")
	}
	if _, err := s.SessionUser(ctx, session); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("a session outlived the password reset: %v", err)
	}
}

// TestStore_EnsureDemoAccountConcurrent: replicas that start together on a
// database without the demo account all succeed and the account is made once.
// This is the nightly restore: every replica restarts at once, each reads no
// row, and each inserts.
func TestStore_EnsureDemoAccountConcurrent(t *testing.T) {
	ctx := t.Context()
	addr := "demo-race-" + uuid.NewString() + "@example.com"
	d := demoAccount(t, addr, sharedPassword)

	const processes = 8
	stores := make([]*auth.Store, processes)
	for i := range stores {
		p := accountStorePool(t, "demo-race")
		if err := p.Ping(ctx); err != nil {
			t.Fatalf("connect process %d: %v", i, err)
		}
		stores[i] = auth.NewStore(p)
	}
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, processes)
	ready.Add(processes)
	done.Add(processes)
	for i, s := range stores {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			errs[i] = s.EnsureDemoAccount(ctx, d)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("process %d did not start: %v", i, err)
		}
	}
	var holders int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, addr).Scan(&holders); err != nil {
		t.Fatalf("count the accounts: %v", err)
	}
	if holders != 1 {
		t.Errorf("%d accounts hold the demo address, want 1", holders)
	}
	if _, err := auth.NewStore(pool).Authenticate(ctx, addr, sharedPassword); err != nil {
		t.Errorf("the demo password does not sign in: %v", err)
	}
}

// TestStore_EnsureDemoAccountUnproved: an unproved registration at the demo
// address becomes the demo account: the operator's password signs in and the
// registrant's no longer does, so squatting the address cannot hold the demo
// account.
func TestStore_EnsureDemoAccountUnproved(t *testing.T) {
	ctx := t.Context()
	addr := "demo-unproved-" + uuid.NewString() + "@example.com"
	register(t, auth.NewStore(pool), addr)
	s := auth.NewStore(accountStorePool(t, "demo-unproved"))

	if err := s.EnsureDemoAccount(ctx, demoAccount(t, addr, "the operator's long password")); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := s.Authenticate(ctx, addr, "the operator's long password"); err != nil {
		t.Errorf("the demo password does not sign in: %v", err)
	}
	if _, err := s.Authenticate(ctx, addr, sharedPassword); err == nil {
		t.Error("the registrant's password signs in to the demo account")
	}
}

// TestStore_EnsureDemoAccountStaff: EnsureDemoAccount refuses a staff member's
// address with ErrDemoAccountIsStaff and leaves the staff member's own password
// working. The demo password would be printed on the sign-in page, and the
// storefront role cannot take staff away.
func TestStore_EnsureDemoAccountStaff(t *testing.T) {
	ctx := t.Context()
	addr := "demo-staff-" + uuid.NewString() + "@example.com"
	u := registerProved(t, auth.NewStore(pool), addr)
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("make the account staff: %v", err)
	}
	s := auth.NewStore(accountStorePool(t, "demo-staff"))

	err := s.EnsureDemoAccount(ctx, demoAccount(t, addr, "a password for the sign-in page"))
	if !errors.Is(err, auth.ErrDemoAccountIsStaff) {
		t.Fatalf("ensure over a staff account = %v, want ErrDemoAccountIsStaff", err)
	}
	if _, err := s.Authenticate(ctx, addr, sharedPassword); err != nil {
		t.Errorf("the staff member's own password stopped working: %v", err)
	}
}

// TestHandler_DemoAccountFixed: with a real store, the demo account is refused
// a change of address, a resend, a change of password and erasure (303 to
// /account?demo=fixed), nothing is mailed, and the sign-in page prints its
// credentials, while the profile and address saves still work and an ordinary
// account keeps every change. It drives the handlers through Authenticate,
// signed in as the demo account and as an ordinary one.
func TestHandler_DemoAccountFixed(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	demoAddr := "demo-shared-" + uuid.NewString() + "@example.com"
	d := demoAccount(t, demoAddr, sharedPassword)
	if err := s.EnsureDemoAccount(ctx, d); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	h.OfferDemoAccount(d)
	b := changeBrowser{t: t, h: h}
	accounts := accountPage(pool)

	page := httptest.NewRecorder()
	h.SignInPage(page, httptest.NewRequestWithContext(ctx, http.MethodGet, "/signin", http.NoBody))
	if body := page.Body.String(); !strings.Contains(body, demoAddr) || !strings.Contains(body, sharedPassword) {
		t.Error("the sign-in page does not print the demo account's credentials")
	}

	demo := b.signIn(demoAddr)
	moveTo := "elsewhere-" + uuid.NewString() + "@example.com"
	refusals := map[string]struct {
		serve http.HandlerFunc
		form  url.Values
	}{
		"change the address":     {h.ChangeEmail, url.Values{"current": {sharedPassword}, "email": {moveTo}}},
		"resend an address link": {h.ResendVerification, url.Values{}},
		"change the password":    {h.ChangePassword, url.Values{"current": {sharedPassword}, "password": {"a password of my own"}, "confirm": {"a password of my own"}}},
		"delete the account":     {h.Erase, url.Values{"confirm": {demoAddr}}},
	}
	for name, action := range refusals {
		res := b.serve(action.serve, cartForm(ctx, "/account", action.form), demo)
		if loc := res.Header().Get("Location"); res.Code != http.StatusSeeOther || loc != "/account?demo=fixed" {
			t.Errorf("%s as the demo account answered %d to %q, want 303 to /account?demo=fixed", name, res.Code, loc)
		}
	}
	if _, err := s.Authenticate(ctx, demoAddr, sharedPassword); err != nil {
		t.Errorf("the demo password stopped working: %v", err)
	}
	var mail int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic IN ($1, $2) AND lower(payload->>'email') IN (lower($3), lower($4))`,
		outbox.TopicEmailVerify.Name(), outbox.TopicPasswordReset.Name(), moveTo, demoAddr).Scan(&mail); err != nil {
		t.Fatalf("count mail: %v", err)
	}
	if mail != 0 {
		t.Errorf("the refused changes queued %d messages", mail)
	}
	notice := b.serve(accounts.Overview, httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?demo=fixed", http.NoBody), demo)
	if want := i18n.T(ctx, i18n.KeyDemoAccountFixed); !strings.Contains(notice.Body.String(), want) {
		t.Errorf("the account page after a refusal does not say %q (status %d)", want, notice.Code)
	}

	for name, action := range map[string]struct {
		serve http.HandlerFunc
		form  url.Values
		want  string
	}{
		"save the profile": {accounts.UpdateProfile, url.Values{"name": {"訪客"}, "phone": {"0912345678"}}, "/account?saved=1"},
		"add an address": {accounts.AddAddress, url.Values{
			"label": {"家"}, "name": {"訪客"}, "phone": {"0912345678"}, "postal_code": {"100"},
			"city": {"臺北市"}, "district": {"中正區"}, "street": {"重慶南路一段 122 號"},
		}, "/account?saved=1"},
	} {
		res := b.serve(action.serve, cartForm(ctx, "/account", action.form), demo)
		if loc := res.Header().Get("Location"); res.Code != http.StatusSeeOther || loc != action.want {
			t.Errorf("%s as the demo account answered %d to %q, want 303 to %q", name, res.Code, loc, action.want)
		}
	}

	ownAddr := "demo-neighbour-" + uuid.NewString() + "@example.com"
	registerProved(t, s, ownAddr)
	own := b.signIn(ownAddr)
	if res := b.askToMove(own, "moved-"+uuid.NewString()+"@example.com"); res.Header().Get("Location") != "/account?address=m%2A%2A%2A%40example.com&email=sent" {
		t.Errorf("an ordinary account asking to move answered %d to %q", res.Code, res.Header().Get("Location"))
	}
	changed := b.serve(h.ChangePassword, cartForm(ctx, "/account/password", url.Values{
		"current": {sharedPassword}, "password": {"a password of my own"}, "confirm": {"a password of my own"},
	}), own)
	if loc := changed.Header().Get("Location"); loc != "/signin?changed=1" {
		t.Errorf("an ordinary account changing its password answered %d to %q", changed.Code, loc)
	}
	if _, err := s.Authenticate(ctx, ownAddr, "a password of my own"); err != nil {
		t.Errorf("an ordinary account's new password does not sign in: %v", err)
	}
}

// TestHandler_ForgotDemoAccount: Forgot queues no reset link for the demo
// account while it queues one for an ordinary account, and registering the demo
// address answers as any taken address does and does not set a password.
func TestHandler_ForgotDemoAccount(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	demoAddr := "demo-forgot-" + uuid.NewString() + "@example.com"
	d := demoAccount(t, demoAddr, sharedPassword)
	if err := s.EnsureDemoAccount(ctx, d); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ownAddr := "demo-forgot-own-" + uuid.NewString() + "@example.com"
	registerProved(t, s, ownAddr)
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	h.OfferDemoAccount(d)

	queued := func(addr string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM outbox_messages m JOIN users u ON u.id::text = m.payload->>'user_id'
			WHERE m.topic = $1 AND lower(u.email) = lower($2)`,
			outbox.TopicPasswordResetRequest.Name(), addr).Scan(&n); err != nil {
			t.Fatalf("count reset requests: %v", err)
		}
		return n
	}
	for _, addr := range []string{demoAddr, ownAddr} {
		rec := httptest.NewRecorder()
		h.Forgot(rec, cartForm(ctx, "/forgot", url.Values{"email": {addr}}))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("forgot for %s answered %d, want 303", addr, rec.Code)
		}
	}
	if n := queued(demoAddr); n != 0 {
		t.Errorf("%d reset requests were queued for the demo account", n)
	}
	if n := queued(ownAddr); n != 1 {
		t.Errorf("%d reset requests were queued for an ordinary account, want 1", n)
	}

	rec := httptest.NewRecorder()
	h.Register(rec, registrationForm(ctx, demoAddr, "a registrant's long password", "/account"))
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/register?sent=1" {
		t.Errorf("registering the demo address answered %d to %q, want what any taken address gets", rec.Code, loc)
	}
	if _, err := s.Authenticate(ctx, demoAddr, "a registrant's long password"); err == nil {
		t.Error("registering the demo address set its password")
	}
}
