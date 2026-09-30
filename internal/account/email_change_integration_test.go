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
	"github.com/koopa0/goen/internal/i18n"
)

// changeBrowser drives the account handlers the way the router does, through
// Authenticate, from a browser holding session when session is not nil.
type changeBrowser struct {
	t *testing.T
	h *account.Handler
}

func (b changeBrowser) serve(handler http.HandlerFunc, req *http.Request, session *http.Cookie) *httptest.ResponseRecorder {
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	b.h.Authenticate(handler).ServeHTTP(rec, req)
	return rec
}

// signIn signs in with the password every registerProved account holds and
// returns the session cookie.
func (b changeBrowser) signIn(addr string) *http.Cookie {
	b.t.Helper()
	rec := httptest.NewRecorder()
	b.h.SignIn(rec, cartForm(b.t.Context(), "/signin", url.Values{
		"email": {addr}, "password": {"a sufficiently long password"}, "next": {"/account"},
	}))
	if rec.Code != http.StatusSeeOther {
		b.t.Fatalf("signing in as %s answered %d, want 303", addr, rec.Code)
	}
	return sessionCookie(b.t, rec)
}

// askToMove asks, signed in with session, to move that account to addr, and
// returns the answer.
func (b changeBrowser) askToMove(session *http.Cookie, addr string) *httptest.ResponseRecorder {
	return b.serve(b.h.ChangeEmail, cartForm(b.t.Context(), "/account/email", url.Values{
		"email": {addr}, "current": {"a sufficiently long password"},
	}), session)
}

// follow posts the confirmation of a mailed link.
func (b changeBrowser) follow(token string, session *http.Cookie) *httptest.ResponseRecorder {
	return b.serve(b.h.Verify, cartForm(b.t.Context(), "/verify", url.Values{"token": {token}}), session)
}

// TestAnAddressChangeLinkProvesNothingForAnyoneButTheAccountThatAsked: anybody
// may ask to move their own account to an address that is not theirs, and the
// link goes to that address's owner, who has every reason to confirm that it is
// theirs. Proved for the asker's account, the address would take the owner's
// later "Sign in with Google" into an account whose password the asker chose.
// So the link proves nothing followed signed out, which asks for a sign-in, or
// signed in as anybody but the account that asked, which is a dead link.
func TestAnAddressChangeLinkProvesNothingForAnyoneButTheAccountThatAsked(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "change-hijack"))
	b := changeBrowser{t: t, h: account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, account.NewStore(pool), "change-asker-"+uuid.NewString()+"@example.com")
	owner := registerProved(t, account.NewStore(pool), "change-owner-"+uuid.NewString()+"@example.com")
	target := "change-target-" + uuid.NewString() + "@example.com"

	if res := b.askToMove(b.signIn(asker.Email), target); res.Code != http.StatusSeeOther {
		t.Fatalf("asking to move the account answered %d, want 303", res.Code)
	}
	token, _ := queuedLink(t, target)

	unproved := func(step string) {
		t.Helper()
		if got := emailOf(t, asker.ID); got != asker.Email {
			t.Errorf("%s: the asker's account moved to %s", step, got)
		}
		var holders int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`,
			target).Scan(&holders); err != nil {
			t.Fatalf("count the accounts at the address: %v", err)
		}
		if holders != 0 {
			t.Errorf("%s: an account holds the address", step)
		}
	}

	signedOut := b.follow(token, nil)
	signIn := "/signin?" + url.Values{"next": {"/verify?" + url.Values{"token": {token}}.Encode()}}.Encode()
	if loc := signedOut.Header().Get("Location"); signedOut.Code != http.StatusSeeOther || loc != signIn {
		t.Errorf("the link followed signed out answered %d to %q, want 303 to %q", signedOut.Code, loc, signIn)
	}
	unproved("followed signed out")

	ownerSession := b.signIn(owner.Email)
	elsewhere := b.follow(token, ownerSession)
	dead := b.follow("not-a-live-link", ownerSession)
	if elsewhere.Code != http.StatusUnprocessableEntity || elsewhere.Code != dead.Code ||
		elsewhere.Body.String() != dead.Body.String() {
		t.Errorf("the link followed by another account answered %d, and a dead link %d; "+
			"want the same refusal", elsewhere.Code, dead.Code)
	}
	if !strings.Contains(elsewhere.Body.String(), i18n.T(ctx, i18n.KeyVerifyDeadTitle)) {
		t.Error("the link followed by another account is not refused as a dead link")
	}
	unproved("followed by another account")

	google, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "change-google-" + uuid.NewString(), Email: target, EmailVerified: true, Name: "Owner",
	})
	if err != nil {
		t.Fatalf("the address's owner signs in with Google: %v", err)
	}
	if google.ID == asker.ID {
		t.Error("the owner's Google sign-in landed in the account that asked for their address")
	}
	if _, err := s.Authenticate(ctx, target, "a sufficiently long password"); !errors.Is(err, account.ErrBadCredentials) {
		t.Errorf("the asker's password opens an account at the address: %v", err)
	}
}

// TestAnAddressChangeLinkTakesTheAccountThatAskedBackThroughSignIn is the other
// half: followed signed out by whoever asked, the link leads through sign-in
// back to itself, and confirmed there it moves the account.
func TestAnAddressChangeLinkTakesTheAccountThatAskedBackThroughSignIn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "change-own"))
	b := changeBrowser{t: t, h: account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, account.NewStore(pool), "change-own-"+uuid.NewString()+"@example.com")
	target := "change-own-to-" + uuid.NewString() + "@example.com"

	if res := b.askToMove(b.signIn(asker.Email), target); res.Code != http.StatusSeeOther {
		t.Fatalf("asking to move the account answered %d, want 303", res.Code)
	}
	token, _ := queuedLink(t, target)

	signedOut := b.follow(token, nil)
	signInPage, err := url.Parse(signedOut.Header().Get("Location"))
	if err != nil || signInPage.Path != "/signin" {
		t.Fatalf("the link followed signed out answered %d to %q, want the sign-in page",
			signedOut.Code, signedOut.Header().Get("Location"))
	}
	signedIn := httptest.NewRecorder()
	b.h.SignIn(signedIn, cartForm(ctx, "/signin", url.Values{
		"email": {asker.Email}, "password": {"a sufficiently long password"},
		"next": {signInPage.Query().Get("next")},
	}))
	back, err := url.Parse(signedIn.Header().Get("Location"))
	if err != nil || signedIn.Code != http.StatusSeeOther || back.Path != "/verify" || back.Query().Get("token") != token {
		t.Fatalf("signing in answered %d to %q, want 303 back to the link", signedIn.Code, signedIn.Header().Get("Location"))
	}

	confirmed := b.follow(back.Query().Get("token"), sessionCookie(t, signedIn))
	if confirmed.Code != http.StatusOK {
		t.Fatalf("the link followed by the account that asked answered %d, want 200", confirmed.Code)
	}
	if got := emailOf(t, asker.ID); got != target {
		t.Errorf("the account is at %s after confirming, want %s", got, target)
	}
}
