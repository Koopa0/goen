package auth

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// TestHashPassword: a password hashes to a salted hash that verifies against it
// and not against a wrong or empty password, and the hash does not contain the
// password. Two hashes of one password differ, or one rainbow table would cover
// every account that shares it.
func TestHashPassword(t *testing.T) {
	t.Parallel()

	const pw = "correct horse battery"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(h, pw) {
		t.Error("the password does not verify against its own hash")
	}
	if VerifyPassword(h, pw+"x") {
		t.Error("a wrong password verified")
	}
	if VerifyPassword(h, "") {
		t.Error("an empty password verified")
	}
	if strings.Contains(h, pw) {
		t.Error("the encoded hash contains the password")
	}

	other, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if other == h {
		t.Error("the same password hashed identically twice; the hash is unsalted, " +
			"so one rainbow table covers every account that shares a password")
	}
	if !VerifyPassword(other, pw) {
		t.Error("the second hash of the same password does not verify")
	}
}

// TestHashPasswordParameters: the encoded hash is an Argon2id string that
// carries its version, memory, time and parallelism parameters in six fields.
func TestHashPasswordParameters(t *testing.T) {
	t.Parallel()

	h, err := HashPassword("a password long enough")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	for _, want := range []string{"$argon2id$", "m=", "t=", "p=", "v="} {
		if !strings.Contains(h, want) {
			t.Errorf("the encoded hash is missing %q: %s", want, h)
		}
	}
	if n := len(strings.Split(h, "$")); n != 6 {
		t.Errorf("the encoded hash has %d fields, want 6", n)
	}
}

// TestVerifyPasswordMalformedHash: VerifyPassword refuses a stored hash that is
// empty, malformed, of another algorithm or version, has unparseable parameters
// or encodings, or has extra fields.
func TestVerifyPasswordMalformedHash(t *testing.T) {
	t.Parallel()

	for _, h := range []string{
		"",
		"not-a-hash",
		"$argon2id$",
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",        // wrong algorithm
		"$argon2id$v=99$m=65536,t=3,p=4$c2FsdA$aGFzaA",      // wrong version
		"$argon2id$v=19$m=bad,t=3,p=4$c2FsdA$aGFzaA",        // unparseable params
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",         // bad salt encoding
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$!!!",         // bad hash encoding
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA$xtra", // too many fields
	} {
		if VerifyPassword(h, "anything") {
			t.Errorf("a malformed hash verified: %q", h)
		}
	}
}

// TestVerifyPasswordUnsafeCost: VerifyPassword refuses a stored hash whose
// Argon2 cost is zero (zero passes or lanes would panic) or unbounded in
// memory, passes or lanes, so a planted hash cannot make verification exhaust
// the server.
func TestVerifyPasswordUnsafeCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		phc  string
	}{
		{
			name: "zero passes would panic",
			phc:  "$argon2id$v=19$m=65536,t=0,p=4$c2FsdA$aGFzaA",
		},
		{
			name: "zero lanes would panic",
			phc:  "$argon2id$v=19$m=65536,t=3,p=0$c2FsdA$aGFzaA",
		},
		{
			name: "unbounded memory",
			phc:  "$argon2id$v=19$m=4294967295,t=3,p=4$c2FsdA$aGFzaA",
		},
		{
			name: "unbounded passes",
			phc:  "$argon2id$v=19$m=65536,t=25,p=4$c2FsdA$aGFzaA",
		},
		{
			name: "unbounded lanes",
			phc:  "$argon2id$v=19$m=65536,t=3,p=33$c2FsdA$aGFzaA",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if VerifyPassword(tt.phc, "anything") {
				t.Error("a PHC string with unsafe Argon2 parameters verified")
			}
		})
	}
}

// TestStore_OverlongPasswordNoRead: Authenticate and ConfirmPassword answer a
// password over MaxPasswordBytes with ErrBadCredentials before reading the
// database, so a 513-byte probe cannot separate a password account from an
// unknown or identity-only account. The closed pool makes the ordering
// deterministic without a timing assertion.
func TestStore_OverlongPasswordNoRead(t *testing.T) {
	t.Parallel()

	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatalf("build a deliberately dead pool: %v", err)
	}
	t.Cleanup(pool.Close)

	pool.Close()
	store := NewStore(pool)
	checks := []struct {
		name  string
		check func() error
	}{
		{name: "authenticate", check: func() error {
			_, authErr := store.Authenticate(t.Context(), "anyone@example.com", strings.Repeat("a", MaxPasswordBytes+1))
			return authErr
		}},
		{name: "confirm password", check: func() error {
			return store.ConfirmPassword(t.Context(), "anyone@example.com", strings.Repeat("a", MaxPasswordBytes+1))
		}},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if checkErr := tt.check(); !errors.Is(checkErr, ErrBadCredentials) {
				t.Fatalf("%s reached the database for an over-long password: %v", tt.name, checkErr)
			}
		})
	}
}

// TestHandler_SignInOverlongAddress: SignIn answers an over-long address with
// the same 422 page and headers as an ordinary bad credential and keeps no
// limiter key for it (a second request is not throttled), so a huge address
// cannot fill the limiter or be told apart from a wrong password.
func TestHandler_SignInOverlongAddress(t *testing.T) {
	store := deadAccountStore(t)
	normalHandler := rateLimitedAccountHandler(store)
	longHandler := rateLimitedAccountHandler(store)
	password := strings.Repeat("x", MaxPasswordBytes+1)
	normalAddr := "nobody@example.com"
	longAddr := strings.Repeat("a", 60<<10) + "@example.com"

	normal := postAccountForm(t, "/signin", url.Values{
		"email": {normalAddr}, "password": {password}, "next": {"/account"},
	}, normalHandler.SignIn)
	overlong := postAccountForm(t, "/signin", url.Values{
		"email": {longAddr}, "password": {password}, "next": {"/account"},
	}, longHandler.SignIn)

	if normal.Code != http.StatusUnprocessableEntity || overlong.Code != normal.Code {
		t.Fatalf("normal/overlong statuses = %d/%d, want both 422", normal.Code, overlong.Code)
	}
	secondNormal := postAccountForm(t, "/signin", url.Values{
		"email": {normalAddr}, "password": {password}, "next": {"/account"},
	}, normalHandler.SignIn)
	secondOverlong := postAccountForm(t, "/signin", url.Values{
		"email": {longAddr}, "password": {password}, "next": {"/account"},
	}, longHandler.SignIn)
	if secondNormal.Code != http.StatusTooManyRequests {
		t.Fatalf("the normal fixture's second request status = %d, want 429; the comparison never reached the limiter", secondNormal.Code)
	}
	if secondOverlong.Code != http.StatusUnprocessableEntity {
		t.Errorf("the overlong fixture's second request status = %d, want the ordinary 422 without a retained limiter key", secondOverlong.Code)
	}

	normalBody := bodyWithSubmittedEmailHidden(t, normal.Body.String(), normalAddr)
	longBody := bodyWithSubmittedEmailHidden(t, overlong.Body.String(), longAddr)
	if normalBody != longBody {
		t.Errorf("overlong and ordinary bad-credential pages differ beyond the echoed email "+
			"(normal %d bytes, overlong %d bytes after normalisation)", len(normalBody), len(longBody))
	}
	if diff := cmp.Diff(normal.Header(), overlong.Header()); diff != "" {
		t.Errorf("overlong and ordinary bad-credential headers differ (-normal +overlong):\n%s", diff)
	}
}

// TestHandler_ForgotOverlongAddress: Forgot answers an over-long address
// exactly as it answers an unusable one (303, same headers, same body) and
// keeps no limiter key for it.
func TestHandler_ForgotOverlongAddress(t *testing.T) {
	store := deadAccountStore(t)
	normalHandler := rateLimitedAccountHandler(store)
	longHandler := rateLimitedAccountHandler(store)
	longAddr := strings.Repeat("a", 60<<10) + "@example.com"

	normal := postAccountForm(t, "/forgot", url.Values{
		"email": {"not-an-address"},
	}, normalHandler.Forgot)
	overlong := postAccountForm(t, "/forgot", url.Values{
		"email": {longAddr},
	}, longHandler.Forgot)

	// A spent allowance is answered as an unspent one, so the limiter itself is
	// asked which address became a key.
	if _, ok := normalHandler.resetLimit.Allow("forgot:not-an-address"); ok {
		t.Fatal("the bounded fixture's address left no spent key; the comparison never reached the limiter")
	}
	if _, ok := longHandler.resetLimit.Allow("forgot:" + email.Clean(longAddr)); !ok {
		t.Error("the overlong address was kept as a limiter key")
	}
	if normal.Code != http.StatusSeeOther || overlong.Code != normal.Code {
		t.Fatalf("normal/overlong statuses = %d/%d, want both 303", normal.Code, overlong.Code)
	}
	if diff := cmp.Diff(normal.Header(), overlong.Header()); diff != "" {
		t.Errorf("overlong and ordinary forgot headers differ (-normal +overlong):\n%s", diff)
	}
	if normal.Body.String() != overlong.Body.String() {
		t.Errorf("forgot response bodies are not byte-identical: normal=%q overlong=%q",
			normal.Body.String(), overlong.Body.String())
	}
}

func deadAccountStore(t *testing.T) *Store {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatalf("build a deliberately dead pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func rateLimitedAccountHandler(store *Store) *Handler {
	return &Handler{
		store: store, log: slog.New(slog.DiscardHandler), google: &google.Client{},
		signinLimit: accountTestLimiter(), guessCeiling: accountTestLimiter(),
		resetLimit: accountTestLimiter(),
	}
}

// TestHandler_RegisterAddressBound: Register refuses a fourth registration of
// one address with 429 and a Retry-After over nine minutes, however the address
// is cased or padded, and still accepts another address, so the form cannot be
// used to fill one inbox. A registration mails the address it names whether or
// not that address has an account; the bound refuses every address the same
// way.
func TestHandler_RegisterAddressBound(t *testing.T) {
	h := NewHandler(deadAccountStore(t), nil, nil, slog.New(slog.DiscardHandler), false, nil)
	const password = "a sufficiently long password"
	register := func(addr string) *httptest.ResponseRecorder {
		return postAccountForm(t, "/register", url.Values{
			"email": {addr}, "password": {password}, "confirm": {password},
		}, h.Register)
	}

	for i := range 3 {
		if code := register("bounded@example.com").Code; code == http.StatusTooManyRequests {
			t.Fatalf("registration %d of one address was refused; the bound is too tight", i+1)
		}
	}
	refused := register(" Bounded@Example.com ")
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("a fourth registration of one address answered %d, want 429", refused.Code)
	} else if wait, err := strconv.Atoi(refused.Header().Get("Retry-After")); err != nil || wait <= 9*60 {
		t.Errorf("a refused address may register again in %q s; want the ten-minute pace of every "+
			"form that mails an address", refused.Header().Get("Retry-After"))
	}
	if code := register("another@example.com").Code; code == http.StatusTooManyRequests {
		t.Error("a different address was refused; the bound is per address")
	}
}

func accountTestLimiter() *ratelimit.Limiter {
	return ratelimit.New(ratelimit.Config{
		Every: time.Hour, Burst: 1, TTL: time.Hour, MaxKeys: 10,
	})
}

func postAccountForm(t *testing.T, path string, form url.Values, serve http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path,
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	serve(w, r)
	return w
}

func bodyWithSubmittedEmailHidden(t *testing.T, body, addr string) string {
	t.Helper()
	needle := `value="` + addr + `"`
	if count := strings.Count(body, needle); count != 1 {
		t.Fatalf("submitted address occurs in %d value attributes, want exactly 1", count)
	}
	return strings.Replace(body, needle, `value="<submitted-email>"`, 1)
}

// TestHandler_SignInPageNextFallback: SignInPage carries /account as the next
// destination when the requested redirect (//\/evil.example) is refused.
// siteurl.Path owns what is safe; the sign-in page owns where a refused
// post-sign-in redirect lands.
func TestHandler_SignInPageNextFallback(t *testing.T) {
	t.Parallel()

	h := &Handler{log: slog.New(slog.DiscardHandler), google: &google.Client{}}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/signin?next=%2F%2F%2Fevil.example", http.NoBody)
	w := httptest.NewRecorder()
	h.SignInPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `name="next" value="/account"`) {
		t.Errorf("a refused sign-in redirect did not carry the /account fallback: %s", body)
	}
}

func TestPasswordError(t *testing.T) {
	t.Parallel()

	if PasswordError("a long enough password") != "" {
		t.Error("a good password was rejected")
	}
	for _, pw := range []string{"", "short", "123456789"} { // 9 runes is one short
		if PasswordError(pw) == "" {
			t.Errorf("password %q was accepted; the floor is %d runes", pw, MinPasswordRunes)
		}
	}
	if PasswordError(strings.Repeat("a", MaxPasswordBytes+1)) == "" {
		t.Error("an unbounded password was accepted")
	}
	if PasswordError("密碼密碼密碼密碼密碼") != "" {
		t.Error("a ten-character Chinese password was rejected")
	}
	// Four Han characters is 12 bytes and 4 runes: a byte floor of 10 admits it.
	if PasswordError("密碼安全") == "" {
		t.Error("a four-character password was accepted; the floor is counting bytes, " +
			"so any short CJK password clears it")
	}
}

// TestCredentials_ValidateRegistration: ValidateRegistration accepts a valid
// registration and refuses a missing or malformed email, a short password, a
// mismatched confirmation and a control character in the name or email, each on
// its own field.
func TestCredentials_ValidateRegistration(t *testing.T) {
	t.Parallel()

	good := Credentials{
		Email: "a@example.com", Password: "a long enough password",
		Confirm: "a long enough password", Name: "王小明",
	}
	if errs := good.ValidateRegistration(); len(errs) != 0 {
		t.Fatalf("a valid registration was rejected: %+v", errs)
	}

	for _, tt := range []struct {
		name  string
		mut   func(*Credentials)
		field string
	}{
		{"no email", func(c *Credentials) { c.Email = "" }, "email"},
		{"bad email", func(c *Credentials) { c.Email = "nope" }, "email"},
		{"short password", func(c *Credentials) { c.Password, c.Confirm = "short", "short" }, "password"},
		{"mismatched confirmation", func(c *Credentials) { c.Confirm = "something else" }, "confirm"},
		{"control character in the name", func(c *Credentials) { c.Name = "王\u0085明" }, "name"},
		{"control character in the email", func(c *Credentials) { c.Email = "a\u0085@example.com" }, "email"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := good
			tt.mut(&c)
			errs := c.ValidateRegistration()
			if len(errs) == 0 {
				t.Fatalf("%s was accepted", tt.name)
			}
			var found bool
			for _, e := range errs {
				if e.Field == tt.field {
					found = true
				}
			}
			if !found {
				t.Errorf("rejected, but not on %q: %+v", tt.field, errs)
			}
			if tt.name == "control character in the email" {
				for _, e := range errs {
					if e.Field == "name" {
						t.Errorf("email-only control character was also attributed to name: %+v", errs)
					}
				}
			}
		})
	}
}

// TestNormaliseGoogleIdentity: normaliseGoogleIdentity trims the display name
// and refuses an identity whose subject is empty, padded or too long, whose
// address is malformed or whose address Google has not verified, but drops an
// oversized name instead of refusing the sign-in.
func TestNormaliseGoogleIdentity(t *testing.T) {
	t.Parallel()

	valid := google.Identity{
		Subject: "google-subject", Email: "person@example.com",
		EmailVerified: true, Name: "  王小明  ",
	}
	got, err := normaliseGoogleIdentity(valid)
	if err != nil {
		t.Fatalf("normalise valid identity: %v", err)
	}
	if got.Name != "王小明" {
		t.Errorf("trimmed display name = %q, want 王小明", got.Name)
	}

	for _, id := range []google.Identity{
		{Subject: "", Email: valid.Email, EmailVerified: true},
		{Subject: " padded ", Email: valid.Email, EmailVerified: true},
		{Subject: strings.Repeat("s", maxOAuthSubjectRunes+1), Email: valid.Email, EmailVerified: true},
		{Subject: "google-subject", Email: "not-an-address", EmailVerified: true},
		{Subject: "google-subject", Email: valid.Email, EmailVerified: false},
	} {
		if _, normaliseErr := normaliseGoogleIdentity(id); normaliseErr == nil {
			t.Errorf("accepted invalid provider identity: %+v", id)
		}
	}

	valid.Name = strings.Repeat("名", user.MaxNameRunes+1)
	got, err = normaliseGoogleIdentity(valid)
	if err != nil {
		t.Fatalf("oversized optional name prevented sign-in: %v", err)
	}
	if got.Name != "" {
		t.Errorf("oversized optional name survived as %d runes", len([]rune(got.Name)))
	}
}

// TestNormaliseUserAgent: normaliseUserAgent keeps a sane user agent (trimmed,
// any UTF-8 within the ceiling) and returns the empty label for one that is
// over the ceiling, holds a newline or is not valid UTF-8, so the stored label
// is bounded and a bad header costs the session its label, not an error.
func TestNormaliseUserAgent(t *testing.T) {
	t.Parallel()
	if got := normaliseUserAgent("  browser/1  "); got != "browser/1" {
		t.Errorf("normaliseUserAgent trimmed value = %q", got)
	}
	if got := normaliseUserAgent(strings.Repeat("a", maxUserAgentRunes)); len(got) != maxUserAgentRunes {
		t.Errorf("exact user-agent ceiling became %d runes", len([]rune(got)))
	}
	if got := normaliseUserAgent("瀏覽器/1"); got != "瀏覽器/1" {
		t.Errorf("a UTF-8 user agent became %q", got)
	}
	for _, raw := range []string{
		strings.Repeat("a", maxUserAgentRunes+1),
		"browser\nforged",
		"Mozilla/5.0 Caf\xe9Browser/1.0",
		"browser/\xe7\x80",
	} {
		if got := normaliseUserAgent(raw); got != "" {
			t.Errorf("unsafe user agent survived as %q", got)
		}
	}
}

// TestPasswordRefusalNoFormatVerb: none of the registration form's field
// messages (email, confirmation and password) renders a format verb, in either
// locale. It asserts the RENDERED string rather than the catalogue: only the
// too-short message carries a verb, and its refusal carries the minimum, so no
// message renders "%!d(MISSING)" or an unfilled verb beside the field.
func TestPasswordRefusalNoFormatVerb(t *testing.T) {
	t.Parallel()

	passwords := map[string]string{
		"password":  "",
		"password2": "short",
		"password3": strings.Repeat("a", MaxPasswordBytes+1),
	}
	every := make([]formtext.FieldRefusal, 0, 4+len(passwords))
	every = append(every,
		formtext.FieldRefusal{Field: "email", MessageKey: i18n.KeyCheckoutEmailRequired},
		formtext.FieldRefusal{Field: "email2", MessageKey: i18n.KeyCheckoutEmailMalformed},
		formtext.FieldRefusal{Field: "email3", MessageKey: i18n.KeyCheckoutEmailTooLong},
		formtext.FieldRefusal{Field: "confirm", MessageKey: i18n.KeyPasswordMismatch})
	for field, password := range passwords {
		refusal, refused := PasswordRefusal(field, password)
		if !refused {
			t.Fatalf("PasswordRefusal(%q, %q) did not refuse", field, password)
		}
		every = append(every, refusal)
	}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		messages := formtext.FieldMessages(ctx, every)
		if len(messages) != len(every) {
			t.Fatalf("%s: %d messages for %d refusals", locale, len(messages), len(every))
		}
		for field, msg := range messages {
			if strings.Contains(msg, "%!") || strings.Contains(msg, "%d") ||
				strings.Contains(msg, "%s") {
				t.Errorf("%s: the %s field renders %q — a format verb reached the "+
					"customer", locale, field, msg)
			}
		}
	}

	// The one message that does carry a verb still gets its number, whichever
	// way registration builds it.
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	creds := Credentials{Email: "a@example.com", Password: "short", Confirm: "short"}
	short := formtext.FieldMessages(ctx, creds.ValidateRegistration())["password"]
	if !strings.Contains(short, strconv.Itoa(MinPasswordRunes)) {
		t.Errorf("the too-short message is %q and does not state the minimum", short)
	}
}

// TestNoticeEraseOpenReturn: Notice explains that an open return blocks erasure
// when the account page is opened with erase=return.
func TestNoticeEraseOpenReturn(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		r := httptest.NewRequestWithContext(ctx, http.MethodGet,
			"/account?erase=return", http.NoBody)
		if got, want := Notice(r), i18n.T(ctx, i18n.KeyEraseOpenReturn); got != want {
			t.Errorf("%s open-return erasure notice = %q, want %q", locale, got, want)
		}
	}
}

// TestNoticeEraseLastAdmin: Notice explains that the last administrator cannot
// be erased when the account page is opened with erase=admin.
func TestNoticeEraseLastAdmin(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		r := httptest.NewRequestWithContext(ctx, http.MethodGet,
			"/account?erase=admin", http.NoBody)
		if got, want := Notice(r), i18n.T(ctx, i18n.KeyEraseLastAdmin); got != want {
			t.Errorf("%s last-admin erasure notice = %q, want %q", locale, got, want)
		}
	}
}

// TestCartRecoveryLanding: cartRecoveryLanding carries a safe continuation into
// the cart-recovery URL and replaces an unsafe one (//evil.example) with
// /account.
func TestCartRecoveryLanding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"/account", "/account/cart-recovery?next=%2Faccount"},
		{"/cart", "/account/cart-recovery?next=%2Fcart"},
		{"/checkout?ship=express", "/account/cart-recovery?next=%2Fcheckout%3Fship%3Dexpress"},
		{"/products/demo#specs", "/account/cart-recovery?next=%2Fproducts%2Fdemo%23specs"},
		{"//evil.example", "/account/cart-recovery?next=%2Faccount"},
	}
	for _, tt := range tests {
		if got := cartRecoveryLanding(tt.input); got != tt.want {
			t.Errorf("cartRecoveryLanding(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestCartAdoptionLanding: cartAdoptionLanding sends an adjusted cart to /cart
// with qty=adjusted and the continuation kept, an unchanged adoption to its
// target, and a failed adoption to cart recovery.
func TestCartAdoptionLanding(t *testing.T) {
	for _, tt := range []struct{ target, want string }{
		{"/cart#items", "/cart?qty=adjusted#items"},
		{"/cart?source=signin#items", "/cart?qty=adjusted&source=signin#items"},
		{"/checkout?source=signin#address", "/cart?next=%2Fcheckout%3Fsource%3Dsignin%23address&qty=adjusted"},
		{"//evil.example", "/cart?next=%2Faccount&qty=adjusted"},
	} {
		if got := cartAdoptionLanding(tt.target, cartAdoptionAdjusted); got != tt.want {
			t.Errorf("adjusted %q = %q, want %q", tt.target, got, tt.want)
		}
	}
	for _, target := range []string{"/cart#items", "/checkout?from=signin#address"} {
		if got := cartAdoptionLanding(target, cartAdoptionUnchanged); got != target {
			t.Errorf("unchanged destination = %q, want %q", got, target)
		}
		if got := cartAdoptionLanding(target, cartAdoptionFailed); got != cartRecoveryLanding(target) {
			t.Errorf("failed adoption lost recovery: %s", got)
		}
	}
}

// TestHandler_VerifyPageAcknowledgement: VerifyPage answers the done=1
// acknowledgement with a page that carries no token, address or cookie and is
// identical on refresh, while a request without the done marker keeps the
// confirmation form and does not claim completion.
func TestHandler_VerifyPageAcknowledgement(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		locale  i18n.Locale
		heading string
		body    string
	}{
		{name: "verification-En", handler: h.VerifyPage, path: "/verify", locale: i18n.En, heading: "Address confirmed", body: "Your email address is confirmed. Use this address to sign in; notices go there too."},
		{name: "verification-ZhHant", handler: h.VerifyPage, path: "/verify", locale: i18n.ZhHant, heading: "\u4fe1\u7bb1\u5df2\u78ba\u8a8d", body: "電子郵件已確認，之後請用這個信箱登入，通知信也會寄到這裡。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			const tok = "private-confirmation-token"
			const address = "private-mailbox@example.com"
			var first string
			for attempt := range 2 {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet,
					tt.path+"?done=1&token="+tok+"&email="+address, http.NoBody)
				res := httptest.NewRecorder()
				tt.handler(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("acknowledgement GET = %d, want 200", res.Code)
				}
				body := res.Body.String()
				for _, want := range []string{tt.heading, tt.body} {
					if !strings.Contains(body, want) {
						t.Errorf("acknowledgement omits %q", want)
					}
				}
				for _, forbidden := range []string{tok, address, `name="token"`, `class="notice__form"`, "%s", "%!("} {
					if strings.Contains(body, forbidden) {
						t.Errorf("acknowledgement contains %q", forbidden)
					}
				}
				if got := res.Header().Values("Set-Cookie"); len(got) != 0 {
					t.Errorf("acknowledgement Set-Cookie = %q, want none", got)
				}
				if attempt == 0 {
					first = body
				} else if body != first {
					t.Error("refresh changed the acknowledgement")
				}
			}
			for _, marker := range []string{"", "0", "yes"} {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet,
					tt.path+"?token="+tok+"&done="+marker, http.NoBody)
				res := httptest.NewRecorder()
				tt.handler(res, req)
				if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `name="token" value="`+tok+`"`) ||
					!strings.Contains(res.Body.String(), `method="post" action="`+tt.path+`"`) {
					t.Errorf("unconfirmed GET done=%q lost the confirmation form: status %d", marker, res.Code)
				}
				if strings.Contains(res.Body.String(), tt.body) {
					t.Errorf("unconfirmed GET done=%q claims completion", marker)
				}
			}
		})
	}
}

// TestHandler_VerifyInvalidToken: Verify answers a post without a valid token
// with a 422 dead-link page that points back to the account email section for a
// new link, in both locales.
func TestHandler_VerifyInvalidToken(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.Verify(res, httptest.NewRequestWithContext(ctx, http.MethodPost, "/verify", http.NoBody))
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid verification = %d, want 422", res.Code)
			}
			heading := "This link is no longer valid"
			reason := "It may have been used already, or be more than two days old."
			if locale == i18n.ZhHant {
				heading = "\u9019\u500b\u9023\u7d50\u5df2\u5931\u6548"
				reason = "\u9023\u7d50\u53ef\u80fd\u5df2\u7d93\u7528\u904e\u6216\u8d85\u904e\u5169\u5929\u3002"
			}
			uitest.AssertEmailLink(t, res.Body.String(), heading, reason, "/account#email-heading")
		})
	}
}

// TestHandler_VerifyFailedTryAgain: verifyFailed with the try-again wording
// renders the notice state with a 422 and does not claim the emailed link is
// dead.
func TestHandler_VerifyFailedTryAgain(t *testing.T) {
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.verifyFailed(res, httptest.NewRequestWithContext(ctx, http.MethodPost, "/verify", http.NoBody),
				i18n.T(ctx, i18n.KeyTryAgainTitle), i18n.T(ctx, i18n.KeyTryAgainBody))
			body := res.Body.String()
			if res.Code != http.StatusUnprocessableEntity || strings.Count(body, `class="goen-medallion"`) != 1 {
				t.Error("infrastructure failure lost its existing notice state")
			}
			if strings.Contains(body, `href="/account#email-heading"`) || strings.Contains(body, i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)) {
				t.Error("infrastructure failure claims that the emailed link is dead")
			}
		})
	}
}

// TestRegistrationLanding: registrationLanding sends a new account to its
// welcome page, keeping a safe next step and the cart-adoption outcome
// (adjusted, failed) and dropping an external one.
func TestRegistrationLanding(t *testing.T) {
	for _, tt := range []struct {
		name, next, want string
		adoption         cartAdoption
	}{
		{name: "default", next: "/account", want: "/account?welcome=1"},
		{name: "cart", next: "/cart", want: "/account?next=%2Fcart&welcome=1"},
		{name: "external", next: "https://example.com/checkout", want: "/account?welcome=1"},
		{name: "adjusted", next: "/checkout", adoption: cartAdoptionAdjusted, want: "/account?adjusted=1&next=%2Fcart%3Fnext%3D%252Fcheckout%26qty%3Dadjusted&welcome=1"},
		{name: "failed", next: "/checkout", adoption: cartAdoptionFailed, want: "/account/cart-recovery?next=%2Faccount%3Fnext%3D%252Fcheckout%26welcome%3D1&welcome=1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := registrationLanding(tt.next, tt.adoption); got != tt.want {
				t.Errorf("registration landing = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPasswordCost: the Argon2id cost of every stored password hash stays at
// the OWASP second option (m=64 MiB, t=3, p=4). HashPassword reads
// passwordCost, which is declared from these constants and which only
// export_test.go may lower, so a change here changes what production spends per
// sign-in and what an attacker must spend per guess.
func TestPasswordCost(t *testing.T) {
	// No t.Parallel: the third check swaps the package variable.
	const reference = "OWASP Password Storage Cheat Sheet, Argon2id second option: " +
		"m=64 MiB (65536 KiB), t=3, p=4; change it deliberately, not by accident"

	t.Run("constants", func(t *testing.T) {
		for _, c := range []struct {
			name      string
			got, want uint64
		}{
			{"argonTime", argonTime, 3},
			{"argonMemory (KiB)", argonMemory, 65536},
			{"argonThreads", argonThreads, 4},
		} {
			if c.got != c.want {
				t.Errorf("%s = %d, want %d (%s)", c.name, c.got, c.want, reference)
			}
		}
	})

	t.Run("nothing assigns passwordCost", func(t *testing.T) {
		assertPasswordCostNeverAssigned(t)
	})

	t.Run("hash carries the declared cost", func(t *testing.T) {
		// Under -tags integration TestMain has already lowered passwordCost.
		saved := passwordCost
		t.Cleanup(func() { passwordCost = saved })
		passwordCost = argon2Cost{time: argonTime, memory: argonMemory, threads: argonThreads}

		h, err := HashPassword("a password long enough")
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		const wantPrefix = "$argon2id$v=19$m=65536,t=3,p=4$"
		if !strings.HasPrefix(h, wantPrefix) {
			t.Errorf("hash %q does not start with %q (%s)", h, wantPrefix, reference)
		}
		if !VerifyPassword(h, "a password long enough") {
			t.Error("a hash at the declared cost does not verify")
		}
	})
}

// assertPasswordCostNeverAssigned parses the package's non-test sources and
// fails unless passwordCost is declared once, from exactly the three argon
// constants, and is assigned nowhere else. Test files are skipped because
// export_test.go lowers it on purpose and is never linked into the program.
func assertPasswordCostNeverAssigned(t *testing.T) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	declarations := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var written []ast.Expr
			switch n := n.(type) {
			case *ast.AssignStmt:
				written = n.Lhs
			case *ast.IncDecStmt:
				written = []ast.Expr{n.X}
			case *ast.RangeStmt:
				written = []ast.Expr{n.Key, n.Value}
			case *ast.UnaryExpr:
				if n.Op == token.AND {
					written = []ast.Expr{n.X}
				}
			case *ast.ValueSpec:
				for _, id := range n.Names {
					if id.Name != "passwordCost" {
						continue
					}
					declarations++
					if declarations == 1 {
						assertPasswordCostDeclaredFromConstants(t, fset, n)
					}
				}
			}
			for _, w := range written {
				if w != nil && rootIdent(w) == "passwordCost" {
					t.Errorf("%s assigns passwordCost; only export_test.go may lower it",
						fset.Position(w.Pos()))
				}
			}
			return true
		})
	}
	if declarations != 1 {
		t.Errorf("passwordCost is declared %d times in non-test files, want exactly 1", declarations)
	}
}

// rootIdent names the variable an assignable expression writes through, so
// that passwordCost.time = 1 counts as an assignment to passwordCost.
func rootIdent(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		default:
			return ""
		}
	}
}

func assertPasswordCostDeclaredFromConstants(t *testing.T, fset *token.FileSet, spec *ast.ValueSpec) {
	t.Helper()

	if len(spec.Values) != 1 {
		t.Fatalf("%s: passwordCost needs exactly one initial value", fset.Position(spec.Pos()))
	}
	lit, ok := spec.Values[0].(*ast.CompositeLit)
	if !ok {
		t.Fatalf("%s: passwordCost must be initialised by an argon2Cost literal", fset.Position(spec.Pos()))
	}
	want := map[string]string{"time": "argonTime", "memory": "argonMemory", "threads": "argonThreads"}
	got := map[string]string{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			t.Fatalf("%s: every field of the passwordCost literal must be keyed", fset.Position(el.Pos()))
		}
		key, _ := kv.Key.(*ast.Ident)
		val, _ := kv.Value.(*ast.Ident)
		if key == nil || val == nil {
			t.Fatalf("%s: passwordCost fields must be named constants", fset.Position(kv.Pos()))
		}
		got[key.Name] = val.Name
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("passwordCost is not declared from the argon constants (-want +got):\n%s", diff)
	}
}

// TestHandler_SignInMissLimits: SignIn charges wrong-password misses against a
// per-account budget for each network (a /24) and a total ceiling across
// networks, so neither one network nor a guess spread over many gets unlimited
// tries at an account. A miss is any password the store refuses before it reads
// the database, so this runs on a dead pool; the integration tests prove the
// right password still gets in.
func TestHandler_SignInMissLimits(t *testing.T) {
	h := NewHandler(deadAccountStore(t), nil, nil, slog.New(slog.DiscardHandler), false, nil)
	miss := strings.Repeat("x", MaxPasswordBytes+1)
	signInFrom := func(remoteAddr, addr string) int {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin",
			strings.NewReader(url.Values{"email": {addr}, "password": {miss}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		h.SignIn(w, r)
		return w.Code
	}

	for i := range 6 {
		if code := signInFrom("203.0.113.9:1", "per-network@example.com"); code != http.StatusUnprocessableEntity {
			t.Fatalf("miss %d answered %d, want 422", i+1, code)
		}
	}
	if code := signInFrom("203.0.113.200:1", "per-network@example.com"); code != http.StatusTooManyRequests {
		t.Errorf("a seventh miss from the same /24 answered %d, want 429", code)
	}
	if code := signInFrom("198.51.100.1:1", "per-network@example.com"); code != http.StatusUnprocessableEntity {
		t.Errorf("a miss from another network answered %d, want 422: one network spent another's budget", code)
	}
	if code := signInFrom("203.0.113.9:1", "someone-else@example.com"); code != http.StatusUnprocessableEntity {
		t.Errorf("a miss at another account answered %d, want 422: the budget is per account", code)
	}

	for n := range 10 {
		if code := signInFrom(fmt.Sprintf("192.0.%d.1:1", n), "ceiling@example.com"); code != http.StatusUnprocessableEntity {
			t.Fatalf("miss %d from network %d answered %d, want 422", n+1, n, code)
		}
	}
	if code := signInFrom("192.0.99.1:1", "ceiling@example.com"); code != http.StatusTooManyRequests {
		t.Errorf("an eleventh miss from a fresh network answered %d, want 429: nothing bounds a spread-out guess", code)
	}
}
