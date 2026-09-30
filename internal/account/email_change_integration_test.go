//go:build integration

package account_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
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

// TestAnAddressChangeAnswersTheSameWhetherOrNotTheAddressIsTaken: any account
// may ask to move to any address, so the answer must not say which addresses
// have an account. A taken address and a free one get the same answer and the
// same headers, from the same statements.
func TestAnAddressChangeAnswersTheSameWhetherOrNotTheAddressIsTaken(t *testing.T) {
	ctx := t.Context()
	asker := registerProved(t, account.NewStore(pool), "change-probe-"+uuid.NewString()+"@example.com")
	taken := registerProved(t, account.NewStore(pool), "change-taken-"+uuid.NewString()+"@example.com").Email
	free := "change-free-" + uuid.NewString() + "@example.com"
	session := changeBrowser{t: t, h: account.NewHandler(account.NewStore(pool), nil,
		slog.New(slog.DiscardHandler), false, nil)}.signIn(asker.Email)

	statements := &statementLog{}
	b := changeBrowser{t: t, h: account.NewHandler(account.NewStore(tracedStorePool(t, statements)), nil,
		slog.New(slog.DiscardHandler), false, nil)}
	ask := func(addr string) (*httptest.ResponseRecorder, []string) {
		statements.take()
		rec := b.askToMove(session, addr)
		return rec, statements.take()
	}
	takenRec, takenSQL := ask(taken)
	freeRec, freeSQL := ask(free)

	if takenRec.Code != http.StatusSeeOther || freeRec.Code != takenRec.Code {
		t.Fatalf("statuses taken/free = %d/%d, want 303 for both", takenRec.Code, freeRec.Code)
	}
	if loc := freeRec.Header().Get("Location"); loc != "/account?email=sent" {
		t.Errorf("a change lands at %q, want /account?email=sent", loc)
	}
	if diff := cmp.Diff(takenRec.Header(), freeRec.Header()); diff != "" {
		t.Errorf("changes to a taken and a free address answer different headers (-taken +free):\n%s", diff)
	}
	if takenRec.Body.String() != freeRec.Body.String() {
		t.Errorf("changes to a taken and a free address answer different bodies: %q and %q",
			takenRec.Body.String(), freeRec.Body.String())
	}
	if len(takenSQL) == 0 {
		t.Fatal("the tracer recorded no statement for a change; the comparison below measures nothing")
	}
	if !slices.Equal(takenSQL, freeSQL) {
		t.Errorf("a taken address sends %d statements and a free one %d; they must be the same "+
			"work, statement for statement:\ntaken: %q\nfree:  %q",
			len(takenSQL), len(freeSQL), takenSQL, freeSQL)
	}
	if state, err := account.NewStore(pool).EmailVerification(ctx, asker.ID); err != nil || state.PendingEmail != free {
		t.Errorf("the account page shows %q pending (%v), want the address last asked for", state.PendingEmail, err)
	}
}

// TestOnlyTheMailboxLearnsThatAnAddressHasAnAccount is the worker's half: the
// link to a free address goes out, and a taken address's owner is told that
// somebody asked for it instead, with nothing that proves or opens anything.
func TestOnlyTheMailboxLearnsThatAnAddressHasAnAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	asker := registerProved(t, s, "change-mail-"+uuid.NewString()+"@example.com")
	owner := registerProved(t, s, "change-mail-owner-"+uuid.NewString()+"@example.com")
	free := "change-mail-free-" + uuid.NewString() + "@example.com"

	deliver := func(addr string) (sent []string, told []email.AccountExists) {
		t.Helper()
		requestVerification(t, s, asker.ID, addr)
		var payload []byte
		if err := pool.QueryRow(ctx, `
			SELECT payload FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)
			ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify, addr).Scan(&payload); err != nil {
			t.Fatalf("read the queued link for %s: %v", addr, err)
		}
		var p email.AddressVerify
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatalf("decode the queued link: %v", err)
		}
		if err := s.DeliverAddressVerify(ctx, &p,
			func(_ context.Context, p *email.AddressVerify) error {
				sent = append(sent, p.Email)
				return nil
			},
			func(_ context.Context, p *email.AccountExists) error {
				told = append(told, *p)
				return nil
			}); err != nil {
			t.Fatalf("deliver the link for %s: %v", addr, err)
		}
		return sent, told
	}

	sent, told := deliver(owner.Email)
	if len(sent) != 0 {
		t.Errorf("the link to move an account to %s went to %v; it is another account's address", owner.Email, sent)
	}
	want := []email.AccountExists{{Email: owner.Email, Name: owner.Name, Change: true}}
	if diff := cmp.Diff(want, told, cmpopts.IgnoreFields(email.AccountExists{}, "Locale")); diff != "" {
		t.Errorf("the address's owner was told (-want +got):\n%s", diff)
	}

	sent, told = deliver(free)
	if !slices.Equal(sent, []string{free}) || len(told) != 0 {
		t.Errorf("a free address was sent %v and %v told; want the link sent to it and nobody told", sent, told)
	}
}
