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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/web"
)

// followUpRegistrations delivers every queued registration naming addr's
// account, as the outbox worker would, with tell standing in for the mail. It
// returns how many there were.
func followUpRegistrations(
	t *testing.T,
	s *account.Store,
	addr string,
	tell func(context.Context, *email.AccountExists) error,
) int {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		UPDATE outbox_messages m SET delivered_at = now()
		FROM users u
		WHERE m.topic = $1 AND m.delivered_at IS NULL
		  AND m.payload->>'user_id' = u.id::text AND lower(u.email) = lower($2)
		RETURNING m.payload`, outbox.TopicRegistration.Name(), addr)
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
		var r outbox.AccountRegistration
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
		ORDER BY id DESC LIMIT 1`, outbox.TopicEmailVerify.Name(), addr).Scan(&token, &next); err != nil {
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

func neverTold(t *testing.T) func(context.Context, *email.AccountExists) error {
	t.Helper()
	return func(_ context.Context, p *email.AccountExists) error {
		t.Errorf("%s was told it already has an account", p.Email)
		return nil
	}
}

func TestRegistrationWelcomeRejectsUnsafeReturnPaths(t *testing.T) {
	for _, next := range []string{"https://elsewhere.invalid/checkout", "//elsewhere.invalid/checkout", "/%zzelsewhere.invalid"} {
		t.Run(next, func(t *testing.T) {
			s := account.NewStore(pool)
			h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
			addr := "register-unsafe-next-" + uuid.NewString() + "@example.com"
			h.Register(httptest.NewRecorder(), registrationForm(t.Context(), addr, cartOwnerPassword, next))
			followed := followRegistrationLink(t, s, addr, func(request *http.Request) *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				h.CompleteRegistration(response, request)
				return response
			})
			if got := followed.Header().Get("Location"); got != "/account?welcome=1" {
				t.Errorf("unsafe registration return = %q, want /account?welcome=1", got)
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/account?welcome=1&next="+url.QueryEscape(next), http.NoBody)
			request.AddCookie(sessionCookie(t, followed))
			page := httptest.NewRecorder()
			h.Authenticate(http.HandlerFunc(h.Overview)).ServeHTTP(page, request)
			if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "elsewhere.invalid") {
				t.Errorf("welcome page exposes unsafe return: status=%d", page.Code)
			}
		})
	}
}

func TestRegistrationWelcomePreservesFailedCartAdoptionForRetry(t *testing.T) {
	ctx := t.Context()
	statements := &statementLog{}
	appPool := tracedStorePool(t, statements)
	if _, err := appPool.Exec(ctx, `SET lock_timeout = '100ms'`); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := appPool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("registration composition role=%q error=%v, want store", role, err)
	}
	s := account.NewStore(appPool)
	addr := "register-cart-recovery-" + uuid.NewString() + "@example.com"
	guestToken := "registration-guest-" + uuid.NewString()
	variant := sellableVariant(t, ctx)
	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`, cart.HashToken(guestToken)).Scan(&guestCart); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 2)`, guestCart, variant); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM carts WHERE id = $1 FOR UPDATE`, guestCart); err != nil {
		t.Fatal(err)
	}
	carts := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
	h := account.NewHandler(s, carts, slog.New(slog.DiscardHandler), false, nil)
	h.Register(httptest.NewRecorder(), registrationForm(ctx, addr, cartOwnerPassword, "/checkout"))
	statements.takeErrors()
	guestCookie := &http.Cookie{Name: "goen_cart", Value: guestToken} //nolint:gosec // G124: the browser's guest-cart request cookie.
	completed := followRegistrationLink(t, s, addr, func(request *http.Request) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		h.CompleteRegistration(response, request.WithContext(ctx))
		return response
	}, guestCookie)
	faults := statements.takeErrors()
	if len(faults) != 1 {
		t.Fatalf("adoption faults=%v, want one actual lock failure", faults)
	}
	lockFault, ok := errors.AsType[*pgconn.PgError](faults[0])
	if !ok || lockFault.Code != "55P03" || ctx.Err() != nil {
		t.Fatalf("adoption fault=%v parent=%v, want one SQLSTATE 55P03 with live request", faults, ctx.Err())
	}
	const welcome = "/account?next=%2Fcheckout&welcome=1"
	wantRecovery := "/account/cart-recovery?next=" + url.QueryEscape(welcome) + "&welcome=1"
	if got := completed.Header().Get("Location"); got != wantRecovery {
		t.Errorf("registration adoption failure = %q, want %q", got, wantRecovery)
	}
	if quantity := cartItemQuantity(t, guestCart, variant); quantity != 2 {
		t.Errorf("failed registration adoption changed guest quantity: %d, want 2", quantity)
	}
	var stillGuest bool
	if err := pool.QueryRow(ctx, `SELECT user_id IS NULL FROM carts WHERE id = $1`, guestCart).Scan(&stillGuest); err != nil || !stillGuest {
		t.Fatalf("failed registration adoption changed guest ownership: guest=%t error=%v", stillGuest, err)
	}
	if _, err := s.Authenticate(ctx, addr, cartOwnerPassword); err != nil {
		t.Errorf("registration did not commit before the adoption fault: %v", err)
	}
	var liveRegistrationTokens int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verifications t JOIN users u ON u.id = t.user_id
	    WHERE u.email = $1`, addr).Scan(&liveRegistrationTokens); err != nil || liveRegistrationTokens != 0 {
		t.Errorf("completed registration left live token count=%d error=%v", liveRegistrationTokens, err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	session := sessionCookie(t, completed)
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, completed.Header().Get("Location"), http.NoBody)
	request.AddCookie(session)
	request.AddCookie(guestCookie)
	page := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.CartRecoveryPage)).ServeHTTP(page, request)
	if !strings.Contains(page.Body.String(), `name="next" value="`+html.EscapeString(welcome)+`"`) {
		t.Error("registration recovery form lost the welcome continuation")
	}
	for _, key := range []i18n.Key{i18n.KeyAccountWelcome, i18n.KeyCartMergeFailed, i18n.KeyCartMergeRetry} {
		if !strings.Contains(page.Body.String(), html.EscapeString(i18n.T(ctx, key))) {
			t.Errorf("registration cart recovery omits %s", key)
		}
	}
	retry := cartForm(ctx, "/account/cart/retry", url.Values{"next": {welcome}})
	retry.AddCookie(session)
	retry.AddCookie(guestCookie)
	response := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.RetryCartAdoption)).ServeHTTP(response, retry)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != welcome {
		t.Errorf("registration cart retry = %d Location %q, want 303 %q", response.Code, response.Header().Get("Location"), welcome)
	}
	if quantity := cartItemQuantity(t, guestCart, variant); quantity != 2 {
		t.Errorf("registration retry lost guest quantity: %d, want 2", quantity)
	}
}

// TestRegistrationAnswersTheSameWhetherOrNotTheAddressIsTaken is the
// registration form's promise not to say who has an account. A taken address
// and a free one get the same answer, the same headers and no session, from the
// same statements, and only the mailbox learns which it was.
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
	for _, loc := range []string{takenRec.Header().Get("Location"), freeRec.Header().Get("Location")} {
		if strings.ContainsAny(loc, "@%") {
			t.Errorf("the redirect %q carries the address, which history and proxy logs keep", loc)
		}
	}
	// The cookie echoes what each visitor typed, so it is the one header allowed to differ.
	takenHeader, freeHeader := takenRec.Header().Clone(), freeRec.Header().Clone()
	takenHeader.Del("Set-Cookie")
	freeHeader.Del("Set-Cookie")
	if diff := cmp.Diff(takenHeader, freeHeader); diff != "" {
		t.Errorf("taken and free registrations answer different headers (-taken +free):\n%s", diff)
	}
	if takenRec.Body.String() != freeRec.Body.String() {
		t.Errorf("taken and free registrations answer different bodies: %q and %q",
			takenRec.Body.String(), freeRec.Body.String())
	}
	// The only cookie a registration sets is the pending-registration one, and it
	// is set whether or not the address is taken; no session before the link.
	for name, rec := range map[string]*httptest.ResponseRecorder{"taken": takenRec, "free": freeRec} {
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "goen_register_sent" {
			t.Errorf("a %s registration set %v, want only goen_register_sent; nobody is signed in before the link is followed", name, cookies)
		}
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
	carts := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false,
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
	if loc := followed.Header().Get("Location"); loc != "/account?next=%2Fcart&welcome=1" {
		t.Errorf("following the link lands at %q, want /account?next=%%2Fcart&welcome=1", loc)
	}
	session := sessionCookie(t, followed)
	for _, locale := range i18n.Locales() {
		lctx := i18n.WithLocale(ctx, locale)
		request := httptest.NewRequestWithContext(lctx, http.MethodGet, followed.Header().Get("Location"), http.NoBody)
		request.AddCookie(session)
		welcome := httptest.NewRecorder()
		h.Authenticate(http.HandlerFunc(h.Overview)).ServeHTTP(welcome, request)
		if welcome.Code != http.StatusOK || !strings.Contains(welcome.Body.String(), html.EscapeString(i18n.T(lctx, i18n.KeyAccountWelcome))) ||
			!strings.Contains(welcome.Body.String(), `href="/cart"`) || !strings.Contains(welcome.Body.String(), html.EscapeString(i18n.T(lctx, i18n.KeyWelcomeReturn))) {
			t.Errorf("registration welcome = %d without welcome copy or original cart action", welcome.Code)
		}
	}

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
	// The refusal carries the still-live token back into the form, so it is
	// served through the compressor the router puts in front and must pass it
	// by.
	for name, password := range map[string]string{
		"no password":      "",
		"a wrong password": "a password somebody else might guess",
	} {
		t.Run(name, func(t *testing.T) {
			req := cartForm(ctx, "/register/complete", url.Values{
				"token": {token}, "next": {"/account"}, "password": {password},
			})
			req.Header.Set("Accept-Encoding", "gzip")
			res := httptest.NewRecorder()
			web.Compress(http.HandlerFunc(h.CompleteRegistration)).ServeHTTP(res, req)
			stillUnproved(t, res)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("answered %d, want 422", res.Code)
			}
			if got := res.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("the refusal carrying the live token is sent with Content-Encoding %q, want identity", got)
			}
			if !strings.Contains(res.Body.String(), token) {
				t.Error("the refusal does not carry the live token back; the check above proves nothing")
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

// TestCompletingARegistrationRefusesALinkThatCompletesNone: completion proves an
// address and signs the browser in on a link and a password. A link that moves
// a proved account to another address completes no registration, and the
// account's own password does not make it one.
func TestCompletingARegistrationRefusesALinkThatCompletesNone(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := registerProved(t, s, "complete-change-"+uuid.NewString()+"@example.com")
	moved := "complete-change-to-" + uuid.NewString() + "@example.com"
	token := requestVerification(t, s, u.ID, moved)

	if _, err := s.CompleteRegistration(ctx, token, "a sufficiently long password"); !errors.Is(err, account.ErrVerifyInvalid) {
		t.Errorf("completing a registration with an address-change link and the account's password = %v, "+
			"want ErrVerifyInvalid", err)
	}
	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, uuid.MustParse(u.ID)).
		Scan(&addr); err != nil {
		t.Fatalf("read the account: %v", err)
	}
	if addr != u.Email {
		t.Errorf("the account moved to %s through registration completion", addr)
	}
}

// TestRegisteringATakenAddressTellsItsOwnerAndChangesNothing: the answer the
// form never gives, that the address has an account, goes to the mailbox, and
// the account keeps its password.
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
	n := followUpRegistrations(t, s, addr, func(_ context.Context, p *email.AccountExists) error {
		told = append(told, p.Email+"|"+p.Name)
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
		outbox.TopicEmailVerify.Name(), addr).Scan(&links); err != nil {
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
	if _, err := s.CompleteReset(ctx, token, "the password chosen by reset"); err != nil {
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

// TestResendingARegistrationLinkAnswersEveryAddressTheSame: asking again is the
// form that would say who has an account, so an unproved account, a proved one
// and an address nobody holds get the same answer, and only the unproved
// account's mailbox is sent a link.
func TestResendingARegistrationLinkAnswersEveryAddressTheSame(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	unproved := "resend-unproved-" + uuid.NewString() + "@example.com"
	proved := "resend-proved-" + uuid.NewString() + "@example.com"
	unknown := "resend-unknown-" + uuid.NewString() + "@example.com"
	register(t, s, unproved)
	registerProved(t, s, proved)

	for _, tt := range []struct {
		addr      string
		wantLinks int
	}{
		{unproved, 1},
		{proved, 0},
		{unknown, 0},
	} {
		rec := httptest.NewRecorder()
		h.ResendRegistration(rec, cartForm(ctx, "/register/resend", url.Values{
			"email": {tt.addr}, "next": {"/account"},
		}))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, want 303", tt.addr, rec.Code)
		}
		want := "/register?sent=1&again=1"
		if got := rec.Header().Get("Location"); got != want {
			t.Errorf("%s: sent to %q, want %q", tt.addr, got, want)
		}
		followUpRegistrations(t, s, tt.addr, neverTold(t))
		var links int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
			outbox.TopicEmailVerify.Name(), tt.addr).Scan(&links); err != nil {
			t.Fatalf("count links: %v", err)
		}
		if links != tt.wantLinks {
			t.Errorf("%s: %d links queued, want %d", tt.addr, links, tt.wantLinks)
		}
	}
}

// TestResendingARegistrationLinkIsBoundedPerAddress: the form mails an address
// whoever names it, so it spends the budget registration spends for that
// address instead of opening a second one.
func TestResendingARegistrationLinkIsBoundedPerAddress(t *testing.T) {
	ctx := t.Context()
	h := account.NewHandler(account.NewStore(pool), nil, slog.New(slog.DiscardHandler), false, nil)
	addr := "resend-bounded-" + uuid.NewString() + "@example.com"

	var last int
	for range 4 {
		rec := httptest.NewRecorder()
		h.ResendRegistration(rec, cartForm(ctx, "/register/resend", url.Values{"email": {addr}}))
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("the fourth resend in a row answered %d, want 429", last)
	}
}
