//go:build integration

package account_test

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

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
	"github.com/koopa0/goen/internal/user"
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
	if !strings.Contains(elsewhere.Body.String(), i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)) {
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

// TestABackOfficeAccountKeepsItsAddress: a staff or admin account does not move
// itself to another address, whether it asks as staff or follows, after
// promotion, a link it asked for as a customer.
func TestABackOfficeAccountKeepsItsAddress(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "change-staff"))
	b := changeBrowser{t: t, h: account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)}
	promote := func(u user.User) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE users SET role = 'staff' WHERE id = $1`, u.ID); err != nil {
			t.Fatalf("promote %s: %v", u.Email, err)
		}
	}

	t.Run("asked as staff", func(t *testing.T) {
		staff := registerProved(t, account.NewStore(pool), "change-staff-"+uuid.NewString()+"@example.com")
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
		staff := registerProved(t, account.NewStore(pool), "change-promoted-"+uuid.NewString()+"@example.com")
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
			ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), addr).Scan(&payload); err != nil {
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

// TestAnAddressIsAnotherAccountsWhateverItsCaseButNeverTheAskersOwn: the worker
// compares addresses as sign-in does, whatever their case, and never counts the
// account that asked, which may be proving the address it already holds.
func TestAnAddressIsAnotherAccountsWhateverItsCaseButNeverTheAskersOwn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	asker := registerProved(t, s, "Case-Asker-"+uuid.NewString()+"@Example.com")
	owner := registerProved(t, s, "Case-Owner-"+uuid.NewString()+"@Example.com")

	deliver := func(addr string) (sent []string, told []email.AccountExists) {
		t.Helper()
		requestVerification(t, s, asker.ID, addr)
		var payload []byte
		if err := pool.QueryRow(ctx, `
			SELECT payload FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)
			ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), addr).Scan(&payload); err != nil {
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

// addressMailBudget is how many requests naming one address NewHandler takes
// before the first refusal.
const addressMailBudget = 3

// TestAnAddressIsMailedABoundedNumberOfTimesWhoeverAsks: every address change
// mails the address it names, the link or its owner a note that somebody asked
// for it, and anybody can have an account with their own mailbox. So requests
// naming one address are bounded per address, however many accounts and
// clients they come from.
func TestAnAddressIsMailedABoundedNumberOfTimesWhoeverAsks(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(accountStorePool(t, "change-bound"))
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	// The route as cmd/goen wires it: per client, 20 at once then one every 3 s.
	authLimit := ratelimit.New(ratelimit.Config{Every: 3 * time.Second, Burst: 20, TTL: time.Hour, MaxKeys: 65_536})
	route := ratelimit.Guard(authLimit, slog.New(slog.DiscardHandler), h.RequireUser(h.ChangeEmail))
	b := changeBrowser{t: t, h: h}
	owner := registerProved(t, account.NewStore(pool), "change-bound-owner-"+uuid.NewString()+"@example.com")

	// deliver hands the asker's queued message to the worker's half, and
	// reports whether the address's owner was told.
	deliver := func(askerID string) bool {
		t.Helper()
		var payload []byte
		if err := pool.QueryRow(ctx, `
			UPDATE outbox_messages m SET delivered_at = now()
			FROM email_verifications v
			WHERE m.topic = $1 AND m.delivered_at IS NULL
			  AND m.dedupe_key = 'verify:' || encode(v.digest, 'hex') AND v.user_id = $2
			RETURNING m.payload`, outbox.TopicEmailVerify.Name(), uuid.MustParse(askerID)).Scan(&payload); err != nil {
			t.Fatalf("take the queued message: %v", err)
		}
		var p email.AddressVerify
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatalf("decode the queued message: %v", err)
		}
		told := false
		if err := s.DeliverAddressVerify(ctx, &p,
			func(context.Context, *email.AddressVerify) error { return nil },
			func(_ context.Context, e *email.AccountExists) error {
				told = told || e.Email == owner.Email
				return nil
			}); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		return told
	}

	letters := 0
	const accounts, perAccount = 3, 10
	for a := range accounts {
		asker := registerProved(t, account.NewStore(pool), "change-bound-asker-"+uuid.NewString()+"@example.com")
		session := b.signIn(asker.Email)
		for range perAccount {
			req := cartForm(ctx, "/account/email", url.Values{
				"email": {owner.Email}, "current": {"a sufficiently long password"},
			})
			req.RemoteAddr = "203.0.113." + strconv.Itoa(10+a) + ":4000"
			req.AddCookie(session)
			rec := httptest.NewRecorder()
			h.Authenticate(route).ServeHTTP(rec, req)
			switch {
			case rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/account?address=c%2A%2A%2A%40example.com&email=sent":
				if deliver(asker.ID) {
					letters++
				}
			case rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "":
			default:
				t.Fatalf("a change request answered %d to %q", rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	if letters > addressMailBudget {
		t.Errorf("%d accounts on %d clients made one customer's address receive %d letters; "+
			"one address may be mailed at most %d times before the first refusal",
			accounts, accounts, letters, addressMailBudget)
	}
}

// TestARefusalForAnAddressSaysNothingAboutIt: the bound is the address's own
// count, whoever holds it, so the refusal once it is spent is the same for an
// address with an account and one without. The budget is spent in the
// spellings of one address the form accepts, which are its capitalisations:
// each is the same mailbox, and none may buy a budget of its own.
func TestARefusalForAnAddressSaysNothingAboutIt(t *testing.T) {
	b := changeBrowser{t: t, h: account.NewHandler(account.NewStore(pool), nil,
		slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, account.NewStore(pool), "change-refused-"+uuid.NewString()+"@example.com")
	taken := registerProved(t, account.NewStore(pool), "change-refused-taken-"+uuid.NewString()+"@example.com").Email
	free := "change-refused-free-" + uuid.NewString() + "@example.com"
	session := b.signIn(asker.Email)

	refusal := func(addr string) *httptest.ResponseRecorder {
		t.Helper()
		spellings := []string{addr, strings.ToUpper(addr), strings.ToUpper(addr[:1]) + addr[1:]}
		for i := range addressMailBudget {
			spelling := spellings[i%len(spellings)]
			if rec := b.askToMove(session, spelling); rec.Code != http.StatusSeeOther {
				t.Fatalf("request %d naming %s answered %d, want 303", i+1, spelling, rec.Code)
			}
		}
		return b.askToMove(session, strings.ToUpper(addr[:len(addr)/2])+addr[len(addr)/2:])
	}
	takenRec, freeRec := refusal(taken), refusal(free)

	if takenRec.Code != http.StatusTooManyRequests || freeRec.Code != takenRec.Code {
		t.Fatalf("once spent, a taken and a free address answered %d and %d, want 429 for both",
			takenRec.Code, freeRec.Code)
	}
	for _, rec := range []*httptest.ResponseRecorder{takenRec, freeRec} {
		wait, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		if err != nil {
			t.Errorf("a refusal carries Retry-After %q", rec.Header().Get("Retry-After"))
			continue
		}
		if wait <= 9*60 {
			t.Errorf("a refused address may be asked for again in %d s; want the ten-minute pace "+
				"of every form that mails an address", wait)
		}
	}
	// Retry-After counts down from each address's own first request, so its
	// value is compared only for presence.
	takenHeader, freeHeader := takenRec.Header().Clone(), freeRec.Header().Clone()
	takenHeader.Del("Retry-After")
	freeHeader.Del("Retry-After")
	if diff := cmp.Diff(takenHeader, freeHeader); diff != "" {
		t.Errorf("the refusals for a taken and a free address differ (-taken +free):\n%s", diff)
	}
	if takenRec.Body.String() != freeRec.Body.String() {
		t.Errorf("the refusals for a taken and a free address answer %q and %q",
			takenRec.Body.String(), freeRec.Body.String())
	}
}

// TestRegistrationAndAnAddressChangeShareOneBudgetPerAddress: both forms mail
// the address they name, so taking turns between them buys no more letters.
func TestRegistrationAndAnAddressChangeShareOneBudgetPerAddress(t *testing.T) {
	ctx := t.Context()
	b := changeBrowser{t: t, h: account.NewHandler(account.NewStore(pool), nil,
		slog.New(slog.DiscardHandler), false, nil)}
	asker := registerProved(t, account.NewStore(pool), "shared-budget-asker-"+uuid.NewString()+"@example.com")
	session := b.signIn(asker.Email)
	addr := "shared-budget-" + uuid.NewString() + "@example.com"
	register := func() int {
		rec := httptest.NewRecorder()
		b.h.Register(rec, registrationForm(ctx, addr, "a sufficiently long password", "/account"))
		return rec.Code
	}

	for i := range addressMailBudget - 1 {
		if code := register(); code != http.StatusSeeOther {
			t.Fatalf("registration %d of the address answered %d, want 303", i+1, code)
		}
	}
	if code := b.askToMove(session, addr).Code; code != http.StatusSeeOther {
		t.Fatalf("the change request that spends the last of the budget answered %d, want 303", code)
	}
	if code := b.askToMove(session, addr).Code; code != http.StatusTooManyRequests {
		t.Errorf("a change request past the address's budget answered %d, want 429", code)
	}
	if code := register(); code != http.StatusTooManyRequests {
		t.Errorf("a registration past the address's budget answered %d, want 429: the change "+
			"requests were not counted against it", code)
	}
}

// TestALinkThatCanNoLongerBeFollowedIsNeverMailed: by the time the worker
// reaches a queued link it may have been replaced by a later request, spent, or
// left to expire. Mailed, it would only be a letter that fails when followed, so
// nothing is sent and nobody is told.
func TestALinkThatCanNoLongerBeFollowedIsNeverMailed(t *testing.T) {
	for name, kill := range map[string]func(t *testing.T, s *account.Store, asker user.User, p *email.AddressVerify){
		"replaced by a later request": func(t *testing.T, s *account.Store, asker user.User, _ *email.AddressVerify) {
			t.Helper()
			requestVerification(t, s, asker.ID, "dead-link-later-"+uuid.NewString()+"@example.com")
		},
		"spent": func(t *testing.T, s *account.Store, asker user.User, p *email.AddressVerify) {
			t.Helper()
			if _, err := s.ConfirmVerification(t.Context(), p.Token, asker.ID); err != nil {
				t.Fatalf("spend the link: %v", err)
			}
		},
		"expired": func(t *testing.T, _ *account.Store, asker user.User, _ *email.AddressVerify) {
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
			s := account.NewStore(pool)
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
			var p email.AddressVerify
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Fatalf("decode the queued link: %v", err)
			}
			kill(t, s, asker, &p)

			var sent []string
			var told []email.AccountExists
			if err := s.DeliverAddressVerify(ctx, &p,
				func(_ context.Context, p *email.AddressVerify) error {
					sent = append(sent, p.Email)
					return nil
				},
				func(_ context.Context, p *email.AccountExists) error {
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

func TestEmailVerificationSuccessfulPostRedirectsBeforeRefresh(t *testing.T) {
	tests := []struct {
		locale  i18n.Locale
		heading string
		body    string
	}{
		{locale: i18n.En, heading: "Address confirmed", body: "Your email address is confirmed. Everything we send you goes there from now on."},
		{locale: i18n.ZhHant, heading: "\u4fe1\u7bb1\u5df2\u78ba\u8a8d", body: "\u96fb\u5b50\u90f5\u4ef6\u5df2\u78ba\u8a8d\uff0c\u4e4b\u5f8c\u7684\u901a\u77e5\u4fe1\u90fd\u6703\u5bc4\u5230\u9019\u500b\u4fe1\u7bb1\u3002"},
	}
	for _, tt := range tests {
		t.Run(string(tt.locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			asker := registerProved(t, account.NewStore(pool), "confirmation-from-"+uuid.NewString()+"@example.com")
			target := "confirmation-to-" + uuid.NewString() + "@example.com"
			statements := &statementLog{}
			b := changeBrowser{t: t, h: account.NewHandler(account.NewStore(tracedStorePool(t, statements)), nil,
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

func TestDeadVerificationLinksOfferRecovery(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, state := range []string{"expired", "spent"} {
			t.Run(locale.Tag()+"/"+state, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s := account.NewStore(pool)
				u := registerProved(t, s, "verify-recovery-"+uuid.NewString()+"@goen.invalid")
				b := changeBrowser{t: t, h: account.NewHandler(account.NewStore(accountStorePool(t, "verify-recovery")), nil, slog.New(slog.DiscardHandler), false, nil)}
				session := b.signIn(u.Email)
				target := "verify-replacement-" + uuid.NewString() + "@goen.invalid"
				token := requestVerification(t, s, u.ID, target)
				if state == "expired" {
					if _, err := pool.Exec(ctx, `UPDATE email_verifications
					    SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
					    WHERE digest = $1`, account.HashToken(token)); err != nil {
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
				pagestest.AssertEmailLink(t, res.Body.String(), heading, reason, "/account#email-heading")
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
