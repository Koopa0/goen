//go:build integration

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/twofactor"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// The second factor guards /admin, but the password alone opens /account, and a
// staff account is the same users row. These tests drive the real router, on
// the real store and admin roles, as an enrolled admin whose session came from
// the password alone, and as the same admin after the code: the four doors that
// end, replace or re-credential the account must refuse the first and serve the
// second, and an ordinary customer must see no difference at all.

const doorPassword = "correct horse battery staple 1"

var doorTOTPKey = bytes.Repeat([]byte{9}, 32)

// doorWorld is one router over the store and admin roles of an owner pool.
type doorWorld struct {
	owner  *pgxpool.Pool
	store  *auth.Store
	admin  *twofactor.Store
	router http.Handler
}

func newDoorWorld(t *testing.T, owner *pgxpool.Pool, totpKey []byte) *doorWorld {
	t.Helper()
	ctx := t.Context()
	storePool, err := openPool(ctx, owner.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	adminPool, err := openAdminPool(ctx, owner.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("disabled payment gateway: %v", err)
	}
	return &doorWorld{
		owner: owner, store: auth.NewStore(storePool),
		admin: twofactor.NewStore(adminPool, doorTOTPKey, nil),
		router: newRouter(&RouterConfig{
			Storefront: StorefrontConfig{StorePool: storePool, Payments: gateway, BaseURL: "http://127.0.0.1"},
			BackOffice: BackOfficeConfig{AdminPool: adminPool, Payments: gateway, Refunder: gateway, TOTPKey: totpKey},
		}, quietLog),
	}
}

// doorUser is an account with a password, a linked Google identity and three
// sessions: the one acting, which came from the password alone, and two others.
type doorUser struct {
	id, email, cookie string
	// hash is the password hash the account was created with.
	hash string
}

func (w *doorWorld) newUser(t *testing.T, role string) doorUser {
	t.Helper()
	ctx := t.Context()
	hash, hashErr := auth.HashPassword(doorPassword)
	if hashErr != nil {
		t.Fatalf("hash password: %v", hashErr)
	}
	u := doorUser{email: role + "-" + uuid.NewString() + "@example.com", hash: hash}
	if err := w.owner.QueryRow(ctx, `
		INSERT INTO users (email, role, password_hash, email_verified_at)
		VALUES ($1, $2, $3, now()) RETURNING id`, u.email, role, hash).Scan(&u.id); err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	if _, err := w.owner.Exec(ctx, `
		INSERT INTO user_identities (user_id, provider, provider_subject) VALUES ($1, 'google', $2)`,
		u.id, "subject-"+uuid.NewString()); err != nil {
		t.Fatalf("link google: %v", err)
	}
	for range 2 {
		if _, err := w.store.StartSession(ctx, u.id, "other device", "127.0.0.1"); err != nil {
			t.Fatalf("start other session: %v", err)
		}
	}
	token, sessionErr := w.store.StartSession(ctx, u.id, "this device", "127.0.0.1")
	if sessionErr != nil {
		t.Fatalf("start session: %v", sessionErr)
	}
	u.cookie = "goen_session=" + token
	return u
}

// enrol takes a staff account to a confirmed credential and returns its secret.
func (w *doorWorld) enrol(t *testing.T, u doorUser) []byte {
	t.Helper()
	ctx := t.Context()
	secret, _, err := w.admin.Begin(ctx, u.id, u.email)
	if err != nil {
		t.Fatalf("begin enrolment: %v", err)
	}
	var mailed string
	if err := w.owner.QueryRow(ctx, `
		SELECT payload->>'code' FROM outbox_messages
		WHERE topic = $1 AND payload->>'email' = $2 ORDER BY id DESC LIMIT 1`,
		outbox.TopicStaffEnrolment.Name(), u.email).Scan(&mailed); err != nil {
		t.Fatalf("read mailed code: %v", err)
	}
	if err := w.admin.Confirm(ctx, u.id, twofactor.Code(secret, twofactor.StepAt(time.Now())), mailed); err != nil {
		t.Fatalf("confirm enrolment: %v", err)
	}
	return secret
}

func (w *doorWorld) do(t *testing.T, method, path, cookie string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Cookie", cookie)
	res := httptest.NewRecorder()
	w.router.ServeHTTP(res, req)
	return res
}

// verify passes the second factor on the session u holds, through /admin/verify
// with a real code, exactly as a staff member does.
func (w *doorWorld) verify(t *testing.T, u doorUser, secret []byte) {
	t.Helper()
	// The enrolment spent this step; the next one is inside the accepted skew.
	code := twofactor.Code(secret, twofactor.StepAt(time.Now())+1)
	res := w.do(t, http.MethodPost, "/admin/verify", u.cookie, url.Values{"code": {code}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin" {
		t.Fatalf("verify: %d %q, want 303 /admin; body: %s", res.Code, res.Header().Get("Location"), res.Body.String())
	}
}

// door is one /account action and the way to see whether it took effect.
type door struct {
	name, path string
	form       func(u doorUser) url.Values
	// landing is where a served request goes.
	landing string
	// took reports whether the action happened to u.
	took func(t *testing.T, w *doorWorld, u doorUser) bool
}

func count(t *testing.T, w *doorWorld, query string, args ...any) int {
	t.Helper()
	var n int
	if err := w.owner.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func doors() []door {
	return []door{
		{
			name: "erase", path: "/account/erase", landing: "/?erased=1",
			form: func(u doorUser) url.Values { return url.Values{"confirm": {u.email}} },
			took: func(t *testing.T, w *doorWorld, u doorUser) bool {
				t.Helper()
				return count(t, w, `SELECT count(*) FROM users WHERE id = $1`, u.id) == 0
			},
		},
		{
			name: "password", path: "/account/password", landing: "/signin?changed=1",
			form: func(doorUser) url.Values {
				return url.Values{"current": {doorPassword}, "password": {"a different long passphrase 2"}, "confirm": {"a different long passphrase 2"}}
			},
			took: func(t *testing.T, w *doorWorld, u doorUser) bool {
				t.Helper()
				return count(t, w, `SELECT count(*) FROM users WHERE id = $1 AND password_hash = $2`, u.id, u.hash) == 0
			},
		},
		{
			name: "sessions", path: "/account/sessions/others/end", landing: "/account?other_devices=signed_out",
			form: func(doorUser) url.Values { return url.Values{} },
			took: func(t *testing.T, w *doorWorld, u doorUser) bool {
				t.Helper()
				return count(t, w, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.id) == 1
			},
		},
		{
			name: "google", path: "/account/google/unlink", landing: "/account?unlinked=1",
			form: func(doorUser) url.Values { return url.Values{} },
			took: func(t *testing.T, w *doorWorld, u doorUser) bool {
				t.Helper()
				return count(t, w, `SELECT count(*) FROM user_identities WHERE user_id = $1`, u.id) == 0
			},
		},
	}
}

// secondAdmin keeps the shared database from ever having one admin left, so a
// refusal below cannot be the last-admin rule answering for the check.
func secondAdmin(t *testing.T, w *doorWorld) {
	t.Helper()
	if _, err := w.owner.Exec(t.Context(),
		`INSERT INTO users (email, role) VALUES ($1, 'admin')`, "colleague-"+uuid.NewString()+"@example.com"); err != nil {
		t.Fatalf("create colleague: %v", err)
	}
}

// TestNewRouterStepUpPasswordOnly: the four account doors that end or replace
// an account (erase, password change, end other sessions, unlink Google) refuse
// a staff or admin session that has not passed the second factor, with 303 to
// /admin/verify, and leave the account, its credentials and its sessions
// unchanged.
//
// The second factor guards /admin, but the password alone opens /account, and a
// staff account is the same users row. The test drives the real router, on the
// real store and admin roles, as an enrolled admin or staff member whose
// session came from the password alone, after checking that /admin turns that
// session away, which is what makes the doors a bypass rather than a feature.
func TestNewRouterStepUpPasswordOnly(t *testing.T) {
	w := newDoorWorld(t, pool, doorTOTPKey)
	secondAdmin(t, w)

	for _, role := range []string{"admin", "staff"} {
		for _, d := range doors() {
			t.Run(role+"/"+d.name+"/password only", func(t *testing.T) {
				u := w.newUser(t, role)
				w.enrol(t, u)
				credentials := count(t, w, `SELECT count(*) FROM staff_totp_credentials WHERE user_id = $1`, u.id)
				sessions := count(t, w, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.id)

				// The back office turns this session away, which is what makes
				// the doors below a bypass rather than a feature.
				if res := w.do(t, http.MethodGet, "/admin", u.cookie, nil); res.Header().Get("Location") != "/admin/verify" {
					t.Fatalf("GET /admin = %d %q, want a redirect to /admin/verify", res.Code, res.Header().Get("Location"))
				}

				res := w.do(t, http.MethodPost, d.path, u.cookie, d.form(u))
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/verify" {
					t.Errorf("POST %s = %d %q, want 303 /admin/verify", d.path, res.Code, res.Header().Get("Location"))
				}
				if d.took(t, w, u) {
					t.Errorf("%s took effect on a session that never passed the second factor", d.name)
				}
				if got := count(t, w, `SELECT count(*) FROM users WHERE id = $1`, u.id); got != 1 {
					t.Errorf("users rows = %d, want the account still present", got)
				}
				if got := count(t, w, `SELECT count(*) FROM staff_totp_credentials WHERE user_id = $1`, u.id); got != credentials {
					t.Errorf("totp credentials = %d, want %d", got, credentials)
				}
				if got := count(t, w, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.id); got != sessions {
					t.Errorf("sessions = %d, want %d", got, sessions)
				}
			})
		}
	}
}

// TestNewRouterStepUpVerified: a staff or admin session that has passed the
// second factor is served by each of the four account doors, and the action
// takes effect.
func TestNewRouterStepUpVerified(t *testing.T) {
	w := newDoorWorld(t, pool, doorTOTPKey)
	secondAdmin(t, w)

	for _, role := range []string{"admin", "staff"} {
		for _, d := range doors() {
			t.Run(role+"/"+d.name+"/verified", func(t *testing.T) {
				u := w.newUser(t, role)
				secret := w.enrol(t, u)
				w.verify(t, u, secret)

				res := w.do(t, http.MethodPost, d.path, u.cookie, d.form(u))
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != d.landing {
					t.Errorf("POST %s = %d %q, want 303 %s", d.path, res.Code, res.Header().Get("Location"), d.landing)
				}
				if !d.took(t, w, u) {
					t.Errorf("%s did not take effect for a verified session", d.name)
				}
			})
		}
	}
}

// TestNewRouterStepUpCustomer: an ordinary customer is served by each of the
// four account doors (erase, password change, end other sessions, unlink
// Google) as before: 303 to the door's landing, and the action taken.
func TestNewRouterStepUpCustomer(t *testing.T) {
	w := newDoorWorld(t, pool, doorTOTPKey)

	for _, d := range doors() {
		t.Run(d.name, func(t *testing.T) {
			u := w.newUser(t, "customer")

			res := w.do(t, http.MethodPost, d.path, u.cookie, d.form(u))
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != d.landing {
				t.Errorf("POST %s = %d %q, want 303 %s", d.path, res.Code, res.Header().Get("Location"), d.landing)
			}
			if !d.took(t, w, u) {
				t.Errorf("%s did not take effect for a customer", d.name)
			}
		})
	}
}

// TestNewRouterStepUpNoFactor: with no TOTP key configured, /admin admits a
// password-only admin session and each of the four account doors serves it and
// takes effect: the doors add no gate the deployment lacks.
//
// With no TOTP key the deployment has no second factor, /admin lets a
// password-only staff session in, and the doors must not invent a gate it
// lacks.
func TestNewRouterStepUpNoFactor(t *testing.T) {
	w := newDoorWorld(t, pool, nil)
	secondAdmin(t, w)

	for _, d := range doors() {
		t.Run(d.name, func(t *testing.T) {
			u := w.newUser(t, "admin")
			if res := w.do(t, http.MethodGet, "/admin", u.cookie, nil); res.Code != http.StatusOK {
				t.Fatalf("GET /admin = %d, want 200 with no second factor configured", res.Code)
			}
			res := w.do(t, http.MethodPost, d.path, u.cookie, d.form(u))
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != d.landing {
				t.Errorf("POST %s = %d %q, want 303 %s", d.path, res.Code, res.Header().Get("Location"), d.landing)
			}
			if !d.took(t, w, u) {
				t.Errorf("%s did not take effect", d.name)
			}
		})
	}
}

// TestNewRouterStepUpLastAdmin: a verified sole admin who erases the account is
// sent to /account?erase=admin and the account remains: the database's
// last-admin rule still applies after step-up.
//
// The last-admin protection is the database's, and a verified session must
// still meet it: step-up opens the door, it does not widen what the door may
// do.
func TestNewRouterStepUpLastAdmin(t *testing.T) {
	owner := dbtest.Pool(t)
	w := newDoorWorld(t, owner, doorTOTPKey)
	u := w.newUser(t, "admin")
	secret := w.enrol(t, u)
	w.verify(t, u, secret)

	res := w.do(t, http.MethodPost, "/account/erase", u.cookie, url.Values{"confirm": {u.email}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/account?erase=admin" {
		t.Errorf("erase of the only admin = %d %q, want 303 /account?erase=admin", res.Code, res.Header().Get("Location"))
	}
	if got := count(t, w, `SELECT count(*) FROM users WHERE id = $1`, u.id); got != 1 {
		t.Errorf("the only admin has %d rows, want 1", got)
	}
}
