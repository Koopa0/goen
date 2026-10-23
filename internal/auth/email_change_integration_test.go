//go:build integration

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// changeBrowser drives the account handlers the way the router does, through
// Authenticate, from a browser holding session when session is not nil.
type changeBrowser struct {
	t *testing.T
	h *auth.Handler
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

// TestHandler_VerifyChangeLinkForeign: an address-change link proves the
// address only for the account that asked: followed signed out it asks for a
// sign-in, and followed by any other account it is a dead link, and the asker's
// account never moves. Anybody may ask to move their own account to an address
// that is not theirs; proved for the asker's account, the address would take
// the owner's later "Sign in with Google" into an account whose password the
// asker chose.
func TestHandler_VerifyChangeLinkForeign(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "change-hijack"))
	b := changeBrowser{t: t, h: auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, auth.NewStore(pool), "change-asker-"+uuid.NewString()+"@example.com")
	owner := registerProved(t, auth.NewStore(pool), "change-owner-"+uuid.NewString()+"@example.com")
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
	if !strings.Contains(elsewhere.Body.String(), i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)) {
		t.Error("the link followed by another account is not refused as a dead link")
	}
	unproved("followed by another account")

	signedIn, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "change-google-" + uuid.NewString(), Email: target, EmailVerified: true, Name: "Owner",
	})
	if err != nil {
		t.Fatalf("the address's owner signs in with Google: %v", err)
	}
	if signedIn.ID == asker.ID {
		t.Error("the owner's Google sign-in landed in the account that asked for their address")
	}
	if _, err := s.Authenticate(ctx, target, "a sufficiently long password"); !errors.Is(err, auth.ErrBadCredentials) {
		t.Errorf("the asker's password opens an account at the address: %v", err)
	}
}

// TestHandler_VerifyChangeLinkOwn: an address-change link followed signed out
// by the account that asked leads through sign-in back to itself, and confirmed
// there it moves the account. This is the other half of
// TestHandler_VerifyChangeLinkForeign.
func TestHandler_VerifyChangeLinkOwn(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "change-own"))
	b := changeBrowser{t: t, h: auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, auth.NewStore(pool), "change-own-"+uuid.NewString()+"@example.com")
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
	if confirmed.Code != http.StatusSeeOther || confirmed.Header().Get("Location") != "/verify?done=1" {
		t.Fatalf("the link followed by the account that asked answered %d to %q, want 303 to /verify?done=1",
			confirmed.Code, confirmed.Header().Get("Location"))
	}
	ack := b.serve(b.h.VerifyPage, httptest.NewRequestWithContext(ctx, http.MethodGet,
		confirmed.Header().Get("Location"), http.NoBody), nil)
	if ack.Code != http.StatusOK || strings.Contains(ack.Body.String(), `class="notice__form"`) ||
		strings.Contains(ack.Body.String(), target) || strings.Contains(ack.Body.String(), token) {
		t.Errorf("verification acknowledgement = %d, want private-data-free 200 without a form", ack.Code)
	}
	if got := emailOf(t, asker.ID); got != target {
		t.Errorf("the account is at %s after confirming, want %s", got, target)
	}
}

// TestHandler_ChangeEmailBackOffice: a staff or admin account does not move
// itself to another address: ChangeEmail refuses it and queues no link, and
// Verify refuses with 422 a link that an account asked for as a customer and
// followed after promotion.
func TestHandler_ChangeEmailBackOffice(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "change-staff"))
	b := changeBrowser{t: t, h: auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	promote := func(u user.User) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, u.ID); err != nil {
			t.Fatalf("promote %s: %v", u.Email, err)
		}
	}

	t.Run("asked as staff", func(t *testing.T) {
		staff := registerProved(t, auth.NewStore(pool), "change-staff-"+uuid.NewString()+"@example.com")
		promote(staff)
		target := "change-staff-to-" + uuid.NewString() + "@example.com"

		res := b.askToMove(b.signIn(staff.Email), target)
		if loc := res.Header().Get("Location"); res.Code != http.StatusSeeOther || loc != "/account?email=staff" {
			t.Errorf("ChangeEmail for a staff account = %d Location %q, want 303 /account?email=staff", res.Code, loc)
		}
		var queued int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
			outbox.TopicEmailVerify.Name(), target).Scan(&queued); err != nil {
			t.Fatalf("count the links mailed to %s: %v", target, err)
		}
		if queued != 0 {
			t.Errorf("ChangeEmail for a staff account queued %d links to %s, want 0", queued, target)
		}
	})

	t.Run("followed after promotion", func(t *testing.T) {
		staff := registerProved(t, auth.NewStore(pool), "change-promoted-"+uuid.NewString()+"@example.com")
		target := "change-promoted-to-" + uuid.NewString() + "@example.com"
		session := b.signIn(staff.Email)
		if res := b.askToMove(session, target); res.Code != http.StatusSeeOther {
			t.Fatalf("asking to move the account answered %d, want 303", res.Code)
		}
		token, _ := queuedLink(t, target)
		promote(staff)

		res := b.follow(token, session)
		if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), html.EscapeString(i18n.T(ctx, i18n.KeyEmailStaffFixed))) {
			t.Errorf("Verify for a promoted account = %d, want 422 with the back-office refusal", res.Code)
		}
		if got := emailOf(t, staff.ID); got != staff.Email {
			t.Errorf("the promoted account moved to %s, want %s", got, staff.Email)
		}
	})
}

// TestHandler_ChangeEmailTakenAddress: ChangeEmail answers a taken and a free
// address with the same status, headers and body and runs the same number of
// statements, so the answer does not say which addresses have an account. Any
// account may ask to move to any address.
func TestHandler_ChangeEmailTakenAddress(t *testing.T) {
	ctx := t.Context()
	asker := registerProved(t, auth.NewStore(pool), "change-probe-"+uuid.NewString()+"@example.com")
	taken := registerProved(t, auth.NewStore(pool), "change-taken-"+uuid.NewString()+"@example.com").Email
	free := "change-free-" + uuid.NewString() + "@example.com"
	session := changeBrowser{t: t, h: auth.NewHandler(auth.NewStore(pool), nil, nil,
		slog.New(slog.DiscardHandler), false, nil)}.signIn(asker.Email)

	statements := &statementLog{}
	b := changeBrowser{t: t, h: auth.NewHandler(auth.NewStore(tracedStorePool(t, statements)), nil, nil,
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
	if loc := freeRec.Header().Get("Location"); loc != "/account?address=c%2A%2A%2A%40example.com&email=sent" {
		t.Errorf("a change lands at %q, want the masked destination at /account", loc)
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

// TestStore_DeliverAddressVerifyTaken: DeliverAddressVerify sends the link to a
// free address, and for an address another account holds tells that account's
// owner that somebody asked instead, with nothing that proves or opens
// anything, so only the mailbox learns the address has an account. This is the
// worker's half of TestHandler_ChangeEmailTakenAddress.
func TestStore_DeliverAddressVerifyTaken(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	asker := registerProved(t, s, "change-mail-"+uuid.NewString()+"@example.com")
	owner := registerProved(t, s, "change-mail-owner-"+uuid.NewString()+"@example.com")
	free := "change-mail-free-" + uuid.NewString() + "@example.com"

	deliver := func(addr string) (sent []string, told []auth.AddressTaken) {
		t.Helper()
		requestVerification(t, s, asker.ID, addr)
		var payload []byte
		if err := pool.QueryRow(ctx, `
			SELECT payload FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)
			ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), addr).Scan(&payload); err != nil {
			t.Fatalf("read the queued link for %s: %v", addr, err)
		}
		var p outbox.AddressVerify
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatalf("decode the queued link: %v", err)
		}
		if err := s.DeliverAddressVerify(ctx, &p,
			func(_ context.Context, p *outbox.AddressVerify) error {
				sent = append(sent, p.Email)
				return nil
			},
			func(_ context.Context, p *auth.AddressTaken) error {
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
	want := []auth.AddressTaken{{Email: owner.Email, Name: owner.Name, Change: true}}
	if diff := cmp.Diff(want, told, cmpopts.IgnoreFields(auth.AddressTaken{}, "Locale")); diff != "" {
		t.Errorf("the address's owner was told (-want +got):\n%s", diff)
	}

	sent, told = deliver(free)
	if !slices.Equal(sent, []string{free}) || len(told) != 0 {
		t.Errorf("a free address was sent %v and %v told; want the link sent to it and nobody told", sent, told)
	}
}

// TestStore_DeliverAddressVerifyCase: DeliverAddressVerify compares addresses
// as sign-in does, whatever their case, and never counts the account that
// asked, which may be proving the address it already holds.
func TestStore_DeliverAddressVerifyCase(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(pool)
	asker := registerProved(t, s, "Case-Asker-"+uuid.NewString()+"@Example.com")
	owner := registerProved(t, s, "Case-Owner-"+uuid.NewString()+"@Example.com")

	deliver := func(addr string) (sent []string, told []auth.AddressTaken) {
		t.Helper()
		requestVerification(t, s, asker.ID, addr)
		var payload []byte
		if err := pool.QueryRow(ctx, `
			SELECT payload FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)
			ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), addr).Scan(&payload); err != nil {
			t.Fatalf("read the queued link for %s: %v", addr, err)
		}
		var p outbox.AddressVerify
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatalf("decode the queued link: %v", err)
		}
		if err := s.DeliverAddressVerify(ctx, &p,
			func(_ context.Context, p *outbox.AddressVerify) error {
				sent = append(sent, p.Email)
				return nil
			},
			func(_ context.Context, p *auth.AddressTaken) error {
				told = append(told, *p)
				return nil
			}); err != nil {
			t.Fatalf("deliver the link for %s: %v", addr, err)
		}
		return sent, told
	}

	sent, told := deliver(strings.ToUpper(owner.Email))
	if len(sent) != 0 || len(told) != 1 || told[0].Email != owner.Email || !told[0].Change {
		t.Errorf("a change to %s, another account's address in another case, sent %v and told %+v; "+
			"want no link and its holder told", strings.ToUpper(owner.Email), sent, told)
	}

	sent, told = deliver(asker.Email)
	if len(told) != 0 || !slices.Equal(sent, []string{strings.ToLower(asker.Email)}) {
		t.Errorf("proving the asker's own address sent %v and told %+v; want the link sent and nobody told",
			sent, told)
	}
}

// addressMailBudget is how many letters one flow sends one address before its
// allowance runs out, and accountMailBudget how many one account asks for:
// NewHandler's addressMailPace and accountMailPace.
const (
	addressMailBudget = 3
	accountMailBudget = 3
)

// TestHandler_ChangeEmailAddressBound: requests to change to one address send
// it a bounded number of letters, however many accounts and clients ask, and
// every request is answered alike. Every address change mails the address it
// names, the link or its owner a note that somebody asked for it, and anybody
// can have an account with their own mailbox.
func TestHandler_ChangeEmailAddressBound(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "change-bound"))
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	// The route as cmd/goen wires it: per client, 20 at once then one every 3 s.
	authLimit := ratelimit.New(ratelimit.Config{Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536})
	route := ratelimit.Guard(authLimit, slog.New(slog.DiscardHandler), h.RequireUser(h.ChangeEmail))
	b := changeBrowser{t: t, h: h}
	owner := registerProved(t, auth.NewStore(pool), "change-bound-owner-"+uuid.NewString()+"@example.com")

	// deliver hands the asker's queued message, if it has one, to the worker's
	// half, and reports whether the address's owner was told.
	deliver := func(askerID string) bool {
		t.Helper()
		var payload []byte
		err := pool.QueryRow(ctx, `
			UPDATE outbox_messages m SET delivered_at = now()
			FROM email_verifications v
			WHERE m.topic = $1 AND m.delivered_at IS NULL
			  AND m.dedupe_key = 'verify:' || encode(v.digest, 'hex') AND v.user_id = $2
			RETURNING m.payload`, outbox.TopicEmailVerify.Name(), uuid.MustParse(askerID)).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return false
		}
		if err != nil {
			t.Fatalf("take the queued message: %v", err)
		}
		var p outbox.AddressVerify
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatalf("decode the queued message: %v", err)
		}
		told := false
		if err := s.DeliverAddressVerify(ctx, &p,
			func(context.Context, *outbox.AddressVerify) error { return nil },
			func(_ context.Context, e *auth.AddressTaken) error {
				told = told || e.Email == owner.Email
				return nil
			}); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		return told
	}

	// Each account asks as often as its own allowance lets it, so together
	// they ask more often than the address's allowance, which is what binds.
	letters := 0
	const accounts = 3
	for a := range accounts {
		asker := registerProved(t, auth.NewStore(pool), "change-bound-asker-"+uuid.NewString()+"@example.com")
		session := b.signIn(asker.Email)
		for range accountMailBudget {
			req := cartForm(ctx, "/account/email", url.Values{
				"email": {owner.Email}, "current": {"a sufficiently long password"},
			})
			req.RemoteAddr = "203.0.113." + strconv.Itoa(10+a) + ":4000"
			req.AddCookie(session)
			rec := httptest.NewRecorder()
			h.Authenticate(route).ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/account?address=c%2A%2A%2A%40example.com&email=sent" {
				t.Fatalf("a change request answered %d to %q, want 303 to the sent notice",
					rec.Code, rec.Header().Get("Location"))
			}
			if deliver(asker.ID) {
				letters++
			}
		}
	}
	if letters != addressMailBudget {
		t.Errorf("%d accounts on %d clients made one customer's address receive %d letters, want %d",
			accounts, accounts, letters, addressMailBudget)
	}
}

// TestHandler_RegisterChangeApart: address changes that spend an address's
// allowance of change letters leave its registration letters whole, so a
// registration of the address still sends its letter.
func TestHandler_RegisterChangeApart(t *testing.T) {
	s := auth.NewStore(pool)
	b := changeBrowser{t: t, h: auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, s, "apart-asker-"+uuid.NewString()+"@example.com")
	session := b.signIn(asker.Email)
	addr := "apart-" + uuid.NewString() + "@example.com"

	for i := range addressMailBudget {
		if code := b.askToMove(session, addr).Code; code != http.StatusSeeOther {
			t.Fatalf("change request %d answered %d, want 303", i+1, code)
		}
	}
	rec := httptest.NewRecorder()
	b.h.Register(rec, registrationForm(t.Context(), addr, "a sufficiently long password", "/account"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("registering the address answered %d, want 303", rec.Code)
	}
	if got := followUpRegistrations(t, s, addr, neverTold(t)); got != 1 {
		t.Errorf("the registration after the change requests queued %d letters, want 1", got)
	}
}

// TestHandler_ChangeEmailAccountBound: one account's change request past its
// allowance, each to a different address, is refused with 429 and the
// account's own Retry-After, and another account is still answered.
func TestHandler_ChangeEmailAccountBound(t *testing.T) {
	s := auth.NewStore(pool)
	b := changeBrowser{t: t, h: auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	asker := b.signIn(registerProved(t, s, "account-bound-"+uuid.NewString()+"@example.com").Email)
	another := b.signIn(registerProved(t, s, "account-bound-another-"+uuid.NewString()+"@example.com").Email)
	elsewhere := func() string { return "account-bound-to-" + uuid.NewString() + "@example.com" }

	for i := range accountMailBudget {
		if code := b.askToMove(asker, elsewhere()).Code; code != http.StatusSeeOther {
			t.Fatalf("change request %d answered %d, want 303", i+1, code)
		}
	}
	refused := b.askToMove(asker, elsewhere())
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("the account's change request past its allowance answered %d, want 429", refused.Code)
	} else if wait, err := strconv.Atoi(refused.Header().Get("Retry-After")); err != nil || wait <= 19*60 {
		t.Errorf("a refused account may ask again in %q s; want the twenty-minute pace",
			refused.Header().Get("Retry-After"))
	}
	if code := b.askToMove(another, elsewhere()).Code; code != http.StatusSeeOther {
		t.Errorf("another account's change request answered %d, want 303", code)
	}
}

// TestStore_DeliverAddressVerifyDead: DeliverAddressVerify mails nothing and
// tells nobody when a queued link has been replaced by a later request, spent
// or left to expire. By the time the worker reaches it, mailed it would only be
// a letter that fails when followed.
func TestStore_DeliverAddressVerifyDead(t *testing.T) {
	for name, kill := range map[string]func(t *testing.T, s *auth.Store, asker user.User, p *outbox.AddressVerify){
		"replaced by a later request": func(t *testing.T, s *auth.Store, asker user.User, _ *outbox.AddressVerify) {
			t.Helper()
			requestVerification(t, s, asker.ID, "dead-link-later-"+uuid.NewString()+"@example.com")
		},
		"spent": func(t *testing.T, s *auth.Store, asker user.User, p *outbox.AddressVerify) {
			t.Helper()
			if _, err := s.ConfirmVerification(t.Context(), p.Token, asker.ID); err != nil {
				t.Fatalf("spend the link: %v", err)
			}
		},
		"expired": func(t *testing.T, _ *auth.Store, asker user.User, _ *outbox.AddressVerify) {
			t.Helper()
			if _, err := pool.Exec(t.Context(), `
				UPDATE email_verifications
				SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
				WHERE user_id = $1`, uuid.MustParse(asker.ID)); err != nil {
				t.Fatalf("age the link: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			s := auth.NewStore(pool)
			asker := registerProved(t, s, "dead-link-"+uuid.NewString()+"@example.com")
			free := "dead-link-to-" + uuid.NewString() + "@example.com"
			requestVerification(t, s, asker.ID, free)
			var payload []byte
			if err := pool.QueryRow(ctx, `
				SELECT payload FROM outbox_messages
				WHERE topic = $1 AND lower(payload->>'email') = lower($2)
				ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), free).Scan(&payload); err != nil {
				t.Fatalf("read the queued link for %s: %v", free, err)
			}
			var p outbox.AddressVerify
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Fatalf("decode the queued link: %v", err)
			}
			kill(t, s, asker, &p)

			var sent []string
			var told []auth.AddressTaken
			if err := s.DeliverAddressVerify(ctx, &p,
				func(_ context.Context, p *outbox.AddressVerify) error {
					sent = append(sent, p.Email)
					return nil
				},
				func(_ context.Context, p *auth.AddressTaken) error {
					told = append(told, *p)
					return nil
				}); err != nil {
				t.Fatalf("deliver the link: %v", err)
			}
			if len(sent) != 0 || len(told) != 0 {
				t.Errorf("a link that can no longer be followed was sent to %v and %v told; want nothing mailed",
					sent, told)
			}
		})
	}
}

// TestHandler_VerifyPostRedirects: a link opened by GET runs no statement and
// changes nothing, the confirming POST answers 303 to /verify?done=1 with no
// cookie before any refresh, the acknowledgement is identical on refresh, and a
// spent link then answers 422 and changes nothing.
func TestHandler_VerifyPostRedirects(t *testing.T) {
	tests := []struct {
		locale  i18n.Locale
		heading string
		body    string
	}{
		{locale: i18n.En, heading: "Address confirmed", body: "Your email address is confirmed. Use this address to sign in; notices go there too."},
		{locale: i18n.ZhHant, heading: "\u4fe1\u7bb1\u5df2\u78ba\u8a8d", body: "電子郵件已確認，之後請用這個信箱登入，通知信也會寄到這裡。"},
	}
	for _, tt := range tests {
		t.Run(string(tt.locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			asker := registerProved(t, auth.NewStore(pool), "confirmation-from-"+uuid.NewString()+"@example.com")
			target := "confirmation-to-" + uuid.NewString() + "@example.com"
			statements := &statementLog{}
			b := changeBrowser{t: t, h: auth.NewHandler(auth.NewStore(tracedStorePool(t, statements)), nil, nil,
				slog.New(slog.DiscardHandler), false, nil)}
			session := b.signIn(asker.Email)
			if res := b.askToMove(session, target); res.Code != http.StatusSeeOther {
				t.Fatalf("request address change = %d, want 303", res.Code)
			}
			token, _ := queuedLink(t, target)
			snapshot := func() string {
				t.Helper()
				var state string
				if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
				    'email', email, 'verified', email_verified_at,
				    'tokens', (SELECT count(*) FROM email_verifications WHERE user_id = users.id),
				    'mail', (SELECT count(*) FROM outbox_messages WHERE lower(payload->>'email') = lower($2)))::text
				    FROM users WHERE id = $1`, asker.ID, target).Scan(&state); err != nil {
					t.Fatalf("snapshot verification state: %v", err)
				}
				return state
			}
			before := snapshot()
			statements.take()
			form := httptest.NewRecorder()
			b.h.VerifyPage(form, httptest.NewRequestWithContext(ctx, http.MethodGet, "/verify?token="+token, http.NoBody))
			if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `name="token" value="`+token+`"`) {
				t.Fatal("token GET lost the verification form")
			}
			if got := statements.take(); len(got) != 0 {
				t.Errorf("token GET ran %d database statements, want none", len(got))
			}
			if diff := cmp.Diff(before, snapshot()); diff != "" {
				t.Fatalf("token GET changed verification state (-before +after):\n%s", diff)
			}
			write := func() *httptest.ResponseRecorder {
				return b.serve(b.h.Verify, cartForm(ctx, "/verify", url.Values{"token": {token}}), session)
			}
			res := write()
			if res.Code != http.StatusSeeOther {
				t.Errorf("successful verification POST = %d, want 303 before refresh", res.Code)
			}
			location := res.Header().Get("Location")
			if location != "/verify?done=1" {
				t.Errorf("successful verification Location = %q, want /verify?done=1", location)
			}
			if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
				t.Errorf("verification Set-Cookie = %q, want none", got)
			}
			if got := emailOf(t, asker.ID); got != target {
				t.Fatalf("verified email = %q, want %q", got, target)
			}
			var remaining int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verifications WHERE user_id = $1`, asker.ID).Scan(&remaining); err != nil {
				t.Fatalf("count remaining verification tokens: %v", err)
			}
			if remaining != 0 {
				t.Fatalf("remaining verification tokens = %d, want 0", remaining)
			}
			committed := snapshot()
			checkAcknowledgement := func() {
				t.Helper()
				var first string
				for attempt := range 2 {
					statements.take()
					ack := httptest.NewRecorder()
					b.h.VerifyPage(ack, httptest.NewRequestWithContext(ctx, http.MethodGet, location, http.NoBody))
					if ack.Code != http.StatusOK {
						t.Errorf("acknowledgement GET = %d, want 200", ack.Code)
					}
					for _, want := range []string{tt.heading, tt.body} {
						if !strings.Contains(ack.Body.String(), want) {
							t.Errorf("verification acknowledgement omits %q", want)
						}
					}
					for _, forbidden := range []string{token, target, `name="token"`, `class="notice__form"`, "%s", "%!("} {
						if strings.Contains(ack.Body.String(), forbidden) {
							t.Errorf("verification acknowledgement contains %q", forbidden)
						}
					}
					if attempt == 0 {
						first = ack.Body.String()
					} else if ack.Body.String() != first {
						t.Error("refresh changed the verification acknowledgement")
					}
					if got := statements.take(); len(got) != 0 {
						t.Errorf("acknowledgement GET ran %d database statements, want none", len(got))
					}
					if diff := cmp.Diff(committed, snapshot()); diff != "" {
						t.Errorf("acknowledgement GET changed verification state (-before +after):\n%s", diff)
					}
				}
			}
			if location == "/verify?done=1" {
				checkAcknowledgement()
			}
			if repeated := write(); repeated.Code != http.StatusUnprocessableEntity {
				t.Errorf("spent verification POST = %d, want 422", repeated.Code)
			}
			if diff := cmp.Diff(committed, snapshot()); diff != "" {
				t.Errorf("spent verification POST changed committed state (-before +after):\n%s", diff)
			}
		})
	}
}

// TestHandler_VerifyDeadLink: a dead verification link (expired or spent) is
// not validated by GET, and a POST answers 422 with a recovery page that does
// not leak the token and changes no account state.
func TestHandler_VerifyDeadLink(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, state := range []string{"expired", "spent"} {
			t.Run(locale.Tag()+"/"+state, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s := auth.NewStore(pool)
				u := registerProved(t, s, "verify-recovery-"+uuid.NewString()+"@goen.invalid")
				b := changeBrowser{t: t, h: auth.NewHandler(auth.NewStore(accountStorePool(t, "verify-recovery")), nil, nil, slog.New(slog.DiscardHandler), false, nil)}
				session := b.signIn(u.Email)
				target := "verify-replacement-" + uuid.NewString() + "@goen.invalid"
				token := requestVerification(t, s, u.ID, target)
				if state == "expired" {
					if _, err := pool.Exec(ctx, `UPDATE email_verifications
					    SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
					    WHERE digest = $1`, bearer.Digest(token)); err != nil {
						t.Fatal(err)
					}
				} else if _, err := s.ConfirmVerification(ctx, token, u.ID); err != nil {
					t.Fatal(err)
				}
				before := accountRecoveryState(t, u.ID)
				page := b.serve(b.h.VerifyPage, httptest.NewRequestWithContext(ctx, http.MethodGet, "/verify?token="+token, http.NoBody), session)
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="token" value="`+token+`"`) {
					t.Fatal("GET validated a dead token instead of presenting the verification form")
				}
				if after := accountRecoveryState(t, u.ID); after != before {
					t.Fatal("scanner GET changed account state")
				}
				res := b.serve(b.h.Verify, cartForm(ctx, "/verify", url.Values{"token": {token}}), session)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("dead verification POST = %d, want 422", res.Code)
				}
				heading, reason := "This link is no longer valid", "It may have been used already, or be more than two days old."
				if locale == i18n.ZhHant {
					heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
					reason = "\u9023\u7d50\u53ef\u80fd\u5df2\u7d93\u7528\u904e\u6216\u8d85\u904e\u5169\u5929\u3002"
				}
				uitest.AssertEmailLink(t, res.Body.String(), heading, reason, "/account#email-heading")
				if strings.Contains(res.Body.String(), token) {
					t.Error("dead verification recovery leaks the token")
				}
				if after := accountRecoveryState(t, u.ID); after != before {
					t.Error("refused verification changed account state")
				}
			})
		}
	}
}
