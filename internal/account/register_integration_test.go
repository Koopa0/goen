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
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ratelimit"
)

// followUpRegistrations delivers every queued registration naming addr's
// account, as the outbox worker would, with tell standing in for the mail. It
// returns how many there were.
func followUpRegistrations(
	t *testing.T,
	s *account.Store,
	addr string,
	tell func(ctx context.Context, locale, address, name string) error,
) int {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		UPDATE outbox_messages m SET delivered_at = now()
		FROM users u
		WHERE m.topic = $1 AND m.delivered_at IS NULL
		  AND m.payload->>'user_id' = u.id::text AND lower(u.email) = lower($2)
		RETURNING m.payload`, outbox.TopicRegistration, addr)
	if err != nil {
		t.Fatalf("take the queued registrations for %s: %v", addr, err)
	}
	var payloads [][]byte
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("read a queued registration: %v", err)
		}
		payloads = append(payloads, payload)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the queued registrations: %v", err)
	}
	for _, payload := range payloads {
		var r account.Registration
		if err := json.Unmarshal(payload, &r); err != nil {
			t.Fatalf("decode a queued registration: %v", err)
		}
		if err := s.FollowUpRegistration(t.Context(), &r, tell); err != nil {
			t.Fatalf("follow the registration up: %v", err)
		}
	}
	return len(payloads)
}

// queuedLink is the newest registration or verification link mailed to addr.
func queuedLink(t *testing.T, addr string) (token, next string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'token', coalesce(payload->>'next', '')
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify, addr).Scan(&token, &next); err != nil {
		t.Fatalf("read the link mailed to %s: %v", addr, err)
	}
	return token, next
}

// followRegistrationLink completes the registration of addr as its registrant
// does: the mailed link, and the password chosen at registration, which is
// cartOwnerPassword for every registration it follows, posted from a browser
// holding cookies through serve. It returns the answer.
func followRegistrationLink(
	t *testing.T,
	s *account.Store,
	addr string,
	serve func(*http.Request) *httptest.ResponseRecorder,
	cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	if n := followUpRegistrations(t, s, addr, neverTold(t)); n != 1 {
		t.Fatalf("%d registrations were queued for %s, want 1", n, addr)
	}
	token, next := queuedLink(t, addr)
	req := cartForm(t.Context(), "/register/complete", url.Values{
		"token": {token}, "next": {next}, "password": {cartOwnerPassword},
	})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := serve(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("following %s's registration link answered %d, want 303; body=%s",
			addr, rec.Code, rec.Body.String())
	}
	return rec
}

func registrationForm(ctx context.Context, addr, password, next string) *http.Request {
	return cartForm(ctx, "/register", url.Values{
		"email": {addr}, "password": {password}, "confirm": {password},
		"name": {"註冊測試"}, "next": {next},
	})
}

func neverTold(t *testing.T) func(context.Context, string, string, string) error {
	t.Helper()
	return func(_ context.Context, _, address, _ string) error {
		t.Errorf("%s was told it already has an account", address)
		return nil
	}
}

// TestRegistrationAnswersTheSameWhetherOrNotTheAddressIsTaken is the
// registration form's promise not to say who has an account. A taken address
// used to be refused in place and a free one signed straight in; now both get
// the same answer, the same headers and no session, from the same statements,
// and only the mailbox learns which it was.
func TestRegistrationAnswersTheSameWhetherOrNotTheAddressIsTaken(t *testing.T) {
	ctx := t.Context()
	taken := "register-taken-" + uuid.NewString() + "@example.com"
	free := "register-free-" + uuid.NewString() + "@example.com"
	registerProved(t, account.NewStore(pool), taken)

	statements := &statementLog{}
	traced := tracedStorePool(t, statements)
	h := account.NewHandler(account.NewStore(traced), nil, slog.New(slog.DiscardHandler), false, nil)
	post := func(addr string) (*httptest.ResponseRecorder, []string) {
		statements.take()
		rec := httptest.NewRecorder()
		h.Register(rec, registrationForm(ctx, addr, "a different long password", "/account"))
		return rec, statements.take()
	}

	takenRec, takenSQL := post(taken)
	freeRec, freeSQL := post(free)

	if takenRec.Code != http.StatusSeeOther || freeRec.Code != takenRec.Code {
		t.Fatalf("statuses taken/free = %d/%d, want 303 for both; taken body=%s",
			takenRec.Code, freeRec.Code, takenRec.Body.String())
	}
	if loc := takenRec.Header().Get("Location"); loc != "/register?sent=1" {
		t.Errorf("a registration lands at %q, want /register?sent=1", loc)
	}
	if diff := cmp.Diff(takenRec.Header(), freeRec.Header()); diff != "" {
		t.Errorf("taken and free registrations answer different headers (-taken +free):\n%s", diff)
	}
	if takenRec.Body.String() != freeRec.Body.String() {
		t.Errorf("taken and free registrations answer different bodies: %q and %q",
			takenRec.Body.String(), freeRec.Body.String())
	}
	if cookies := freeRec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("a registration set %v; nobody is signed in before the link is followed", cookies)
	}
	if len(takenSQL) == 0 {
		t.Fatal("the tracer recorded no statement for a registration; the comparison below measures nothing")
	}
	if !slices.Equal(takenSQL, freeSQL) {
		t.Errorf("a taken address sends %d statements and a free one %d; they must be the same "+
			"work, statement for statement:\ntaken: %q\nfree:  %q",
			len(takenSQL), len(freeSQL), takenSQL, freeSQL)
	}
}

// TestARegistrationIsUsableOnlyOnceItsLinkIsFollowed is the other half: the
// account a registration creates cannot be signed into with its password until
// the mailed link is followed with that password, and following it signs that
// browser in, adopts its cart and lands where the registration was headed.
func TestARegistrationIsUsableOnlyOnceItsLinkIsFollowed(t *testing.T) {
	ctx := t.Context()
	appPool := accountStorePool(t, "registration-link")
	s := account.NewStore(appPool)
	carts := cart.NewHandler(cart.NewStore(appPool), slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}),
		nil, nil)
	h := account.NewHandler(s, carts, slog.New(slog.DiscardHandler), false, nil)
	addr := "register-link-" + uuid.NewString() + "@example.com"
	const password = "a sufficiently long password"

	added := httptest.NewRecorder()
	carts.AddItem(added, cartForm(ctx, "/cart/items", url.Values{
		"variant": {sellableVariant(t, ctx).String()}, "quantity": {"1"},
	}))
	guestCart := lastCartCookie(t, added)

	registered := httptest.NewRecorder()
	req := registrationForm(ctx, addr, password, "/cart")
	req.AddCookie(guestCart)
	h.Register(registered, req)
	if registered.Code != http.StatusSeeOther {
		t.Fatalf("registration status = %d, want 303; body=%s", registered.Code, registered.Body.String())
	}
	if _, err := s.Authenticate(ctx, addr, password); !errors.Is(err, account.ErrBadCredentials) {
		t.Fatalf("the registered password signs in before the link is followed: %v", err)
	}

	if n := followUpRegistrations(t, s, addr, neverTold(t)); n != 1 {
		t.Fatalf("%d registrations were queued for a new address, want 1", n)
	}
	token, next := queuedLink(t, addr)
	if next != "/cart" {
		t.Errorf("the mailed link carries next %q, want the registration's /cart", next)
	}

	page := httptest.NewRecorder()
	h.CompleteRegistrationPage(page, httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/register/complete?"+url.Values{"token": {token}, "next": {next}}.Encode(), http.NoBody))
	for _, want := range []string{`name="next" value="/cart"`, `name="password"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("the link's page does not carry %s:\n%s", want, page.Body.String())
		}
	}

	confirm := cartForm(ctx, "/register/complete", url.Values{
		"token": {token}, "next": {next}, "password": {password},
	})
	confirm.AddCookie(guestCart)
	followed := httptest.NewRecorder()
	h.CompleteRegistration(followed, confirm)
	if followed.Code != http.StatusSeeOther {
		t.Fatalf("following the link answered %d, want 303; body=%s", followed.Code, followed.Body.String())
	}
	if loc := followed.Header().Get("Location"); loc != "/cart" {
		t.Errorf("following the link lands at %q, want /cart", loc)
	}
	sessionCookie(t, followed)

	var owner uuid.NullUUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM carts WHERE token_hash = $1`,
		cart.HashToken(guestCart.Value)).Scan(&owner); err != nil {
		t.Fatalf("read the guest cart: %v", err)
	}
	if !owner.Valid {
		t.Error("following the link did not adopt the browser's cart")
	}
	if _, err := s.Authenticate(ctx, addr, password); err != nil {
		t.Errorf("the password does not sign in after the link was followed: %v", err)
	}
}

// TestARegistrationLinkWithoutItsPasswordProvesNothing: the link goes to the
// mailbox, and the password to whoever registered. Somebody may register an
// address that is not theirs; if its owner then follows the link and uses the
// account, their addresses and orders sit behind a password somebody else
// chose. So the link alone signs nobody in and proves nothing, and neither
// does it with a password other than the one chosen at registration.
func TestARegistrationLinkWithoutItsPasswordProvesNothing(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	addr := "register-claim-" + uuid.NewString() + "@example.com"
	const chosen = "the password chosen at registration"

	h.Register(httptest.NewRecorder(), registrationForm(ctx, addr, chosen, "/account"))
	if n := followUpRegistrations(t, s, addr, neverTold(t)); n != 1 {
		t.Fatalf("%d registrations were queued, want 1", n)
	}
	token, _ := queuedLink(t, addr)

	stillUnproved := func(t *testing.T, res *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range res.Result().Cookies() {
			if strings.HasSuffix(c.Name, "goen_session") && c.Value != "" && c.MaxAge >= 0 {
				t.Errorf("the answer signed the browser in (%s)", c.Name)
			}
		}
		var proved bool
		if err := pool.QueryRow(ctx, `
			SELECT email_verified_at IS NOT NULL FROM users WHERE lower(email) = lower($1)`,
			addr).Scan(&proved); err != nil {
			t.Fatalf("read the account: %v", err)
		}
		if proved {
			t.Error("the address reads as proved")
		}
		if _, err := s.Authenticate(ctx, addr, chosen); !errors.Is(err, account.ErrBadCredentials) {
			t.Errorf("the registered password signs in: %v", err)
		}
	}

	t.Run("the link alone", func(t *testing.T) {
		res := httptest.NewRecorder()
		h.Verify(res, cartForm(ctx, "/verify", url.Values{"token": {token}, "next": {"/account"}}))
		stillUnproved(t, res)
		if loc := res.Header().Get("Location"); res.Code != http.StatusSeeOther ||
			!strings.HasPrefix(loc, "/register/complete?") {
			t.Errorf("the link alone answered %d to %q, want 303 to the page that asks for the password",
				res.Code, loc)
		}
	})

	// Answered as sign-in answers a wrong password, and the link survives it.
	for name, password := range map[string]string{
		"no password":      "",
		"a wrong password": "a password somebody else might guess",
	} {
		t.Run(name, func(t *testing.T) {
			res := httptest.NewRecorder()
			h.CompleteRegistration(res, cartForm(ctx, "/register/complete", url.Values{
				"token": {token}, "next": {"/account"}, "password": {password},
			}))
			stillUnproved(t, res)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("answered %d, want 422", res.Code)
			}
			if !strings.Contains(res.Body.String(), html.EscapeString(i18n.T(ctx, i18n.KeyBadCredentials))) {
				t.Error("the refusal is not sign-in's refusal of a wrong password")
			}
		})
	}

	// The control: the refusals above were about the password, not a dead link.
	res := httptest.NewRecorder()
	h.CompleteRegistration(res, cartForm(ctx, "/register/complete", url.Values{
		"token": {token}, "next": {"/account"}, "password": {chosen},
	}))
	if res.Code != http.StatusSeeOther {
		t.Fatalf("the link with the chosen password answered %d, want 303", res.Code)
	}
	if _, err := s.Authenticate(ctx, addr, chosen); err != nil {
		t.Errorf("the chosen password does not sign in once the registration is complete: %v", err)
	}
}

// TestRegisteringATakenAddressTellsItsOwnerAndChangesNothing: the answer the
// form no longer gives goes to the mailbox, and the account keeps its password.
func TestRegisteringATakenAddressTellsItsOwnerAndChangesNothing(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	addr := "register-owner-" + uuid.NewString() + "@example.com"
	registerProved(t, s, addr)

	rec := httptest.NewRecorder()
	h.Register(rec, registrationForm(ctx, strings.ToUpper(addr), "an intruder's long password", "/account"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("registration status = %d, want 303", rec.Code)
	}

	var told []string
	n := followUpRegistrations(t, s, addr, func(_ context.Context, _, address, name string) error {
		told = append(told, address+"|"+name)
		return nil
	})
	if n != 1 {
		t.Fatalf("%d registrations were queued, want 1", n)
	}
	if want := []string{addr + "|測試"}; !slices.Equal(told, want) {
		t.Errorf("the owner was told %v, want %v: once, at the account's own address", told, want)
	}
	var links int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
		outbox.TopicEmailVerify, addr).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 {
		t.Errorf("a registration of a taken address queued %d links into that account", links)
	}
	if _, err := s.Authenticate(ctx, addr, "a sufficiently long password"); err != nil {
		t.Errorf("the owner's password stopped working: %v", err)
	}
	if _, err := s.Authenticate(ctx, addr, "an intruder's long password"); err == nil {
		t.Error("the second registration's password signs into the existing account")
	}
}

// TestAResetProvesTheAddressItWasMailedTo: an account that has never proved its
// address — a new colleague, or a registration whose link was lost — signs in
// once a reset link, which only that mailbox received, has been spent.
func TestAResetProvesTheAddressItWasMailedTo(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	addr := "reset-proves-" + uuid.NewString() + "@example.com"
	u := register(t, s, addr)

	token := beginReset(t, s, addr)
	if err := s.CompleteReset(ctx, token, "the password chosen by reset"); err != nil {
		t.Fatalf("CompleteReset: %v", err)
	}
	state, err := s.EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if !state.Verified {
		t.Error("the address is unproved after a link mailed to it was spent")
	}
	if _, err := s.Authenticate(ctx, addr, "the password chosen by reset"); err != nil {
		t.Errorf("the reset password does not sign in: %v", err)
	}
}
