package account

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestPasswordHashingRoundTrips(t *testing.T) {
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

func TestHashCarriesItsParameters(t *testing.T) {
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

func TestVerifyRejectsMalformedHashes(t *testing.T) {
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

func TestVerifyRejectsUnsafeArgonParameters(t *testing.T) {
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

// A password over MaxPasswordBytes is refused before the read, or a 513-byte
// probe separates a password account from an unknown or identity-only account.
// The dead pool makes the ordering deterministic without a timing assertion.
func TestAnOverLongPasswordIsRefusedBeforeTheRead(t *testing.T) {
	t.Parallel()

	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatalf("build a deliberately dead pool: %v", err)
	}
	t.Cleanup(pool.Close)

	_, err = NewStore(pool).Authenticate(t.Context(), "anyone@example.com",
		strings.Repeat("a", MaxPasswordBytes+1))
	if !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("Authenticate reached the database for an over-long password: %v", err)
	}
}

func TestAnOverlongSignInAddressSkipsTheLimiterAndUsesTheOrdinaryFailure(t *testing.T) {
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

func TestAnOverlongForgotAddressIsAnsweredIdenticallyBeforeTheLimiter(t *testing.T) {
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

	secondNormal := postAccountForm(t, "/forgot", url.Values{
		"email": {"not-an-address"},
	}, normalHandler.Forgot)
	secondOverlong := postAccountForm(t, "/forgot", url.Values{
		"email": {longAddr},
	}, longHandler.Forgot)
	if secondNormal.Code != http.StatusTooManyRequests {
		t.Fatalf("the bounded fixture's second request status = %d, want 429; the comparison never reached the limiter", secondNormal.Code)
	}
	if secondOverlong.Code != http.StatusSeeOther {
		t.Errorf("the overlong fixture's second request status = %d, want the ordinary 303 without a retained limiter key", secondOverlong.Code)
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
		store: store, log: slog.New(slog.DiscardHandler), google: &Google{},
		signinLimit: accountTestLimiter(), resetLimit: accountTestLimiter(),
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

func TestHashTokenIsNotTheToken(t *testing.T) {
	t.Parallel()

	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if len(HashToken(tok)) != 32 {
		t.Error("HashToken is not a SHA-256 digest")
	}
	if string(HashToken(tok)) == tok {
		t.Error("HashToken returns the token; the database would hold live sessions")
	}
	// Captured first: comparing two calls in one expression is a tautology.
	first := string(HashToken(tok))
	if string(HashToken(tok)) != first {
		t.Error("HashToken is not deterministic; a returning visitor would lose their session")
	}

	other, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if string(HashToken(other)) == first {
		t.Error("two different tokens hash the same")
	}
}

func TestNewTokenIsUnpredictable(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for range 64 {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token %q is %d characters; too little entropy for a session", tok, len(tok))
		}
		if seen[tok] {
			t.Fatal("NewToken repeated a token")
		}
		seen[tok] = true
	}
}

// TestSignInKeepsItsAccountFallback locks the caller's choice. web.SitePath
// owns what is safe; account owns where a refused post-sign-in redirect lands.
func TestSignInKeepsItsAccountFallback(t *testing.T) {
	t.Parallel()

	h := &Handler{log: slog.New(slog.DiscardHandler), google: &Google{}}
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

func TestValidateRegistration(t *testing.T) {
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
		})
	}
}

// TestNoFieldMessageLeaksAFormatVerb reads what the form actually renders.
//
// fieldMessages formatted every message with MinPasswordRunes, and only the
// too-short one carries a verb — so the six that do not rendered
// "%!(EXTRA int=10)" beside the field, in both locales, on the first form the
// site shows anybody and the one where they decide whether to trust it with a
// password.
//
// It asserts the RENDERED string rather than the catalogue, because the
// catalogue was correct: every message was translated, and the defect was in
// what the handler did with it afterwards.
func TestNoFieldMessageLeaksAFormatVerb(t *testing.T) {
	t.Parallel()

	every := []FieldError{
		{Field: "email", MessageKey: i18n.KeyCheckoutEmailRequired},
		{Field: "email2", MessageKey: i18n.KeyCheckoutEmailMalformed},
		{Field: "email3", MessageKey: i18n.KeyCheckoutEmailTooLong},
		{Field: "password", MessageKey: i18n.KeyPasswordRequired},
		{Field: "password2", MessageKey: i18n.KeyPasswordTooShort},
		{Field: "password3", MessageKey: i18n.KeyPasswordTooLong},
		{Field: "confirm", MessageKey: i18n.KeyPasswordMismatch},
	}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for field, msg := range fieldMessages(ctx, every) {
			if strings.Contains(msg, "%!") || strings.Contains(msg, "%d") ||
				strings.Contains(msg, "%s") {
				t.Errorf("%s: the %s field renders %q — a format verb reached the "+
					"customer", locale, field, msg)
			}
		}
	}

	// The one message that does carry a verb still gets its number.
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	short := fieldMessages(ctx, []FieldError{
		{Field: "password", MessageKey: i18n.KeyPasswordTooShort},
	})["password"]
	if !strings.Contains(short, strconv.Itoa(MinPasswordRunes)) {
		t.Errorf("the too-short message is %q and does not state the minimum", short)
	}
}

// TestAGuestSavingIsSentBackToTheProduct holds where a refused save lands.
//
// Saving is a plain POST, so a signed-out visitor pressing it was redirected to
// /signin?next=/account/wishlist — a fixed string. They signed in and arrived at
// an empty list, having lost both the item they wanted and the page they were
// reading. The form has carried a validated same-site return path since it was
// written; the guest branch simply ran before the form was read.
func TestAGuestSavingIsSentBackToTheProduct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ret  string
		want string
	}{
		{
			name: "back to the product",
			ret:  "/p/pixelight-9-pro",
			want: "/signin?next=/p/pixelight-9-pro",
		},
		{
			// An off-site return is what SitePathOr exists to refuse, and the
			// wishlist is the honest fallback rather than somebody else's host.
			name: "an off-site return is refused",
			ret:  "https://evil.example/steal",
			want: "/signin?next=/account/wishlist",
		},
		{
			name: "a protocol-relative path is refused too",
			ret:  "//evil.example/steal",
			want: "/signin?next=/account/wishlist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{log: slog.New(slog.DiscardHandler)}
			body := url.Values{"slug": {"pixelight-9-pro"}, "return": {tt.ret}}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/wishlist",
				strings.NewReader(body.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			h.SaveWishlist(w, r)

			if w.Code != http.StatusSeeOther {
				t.Fatalf("SaveWishlist(guest) status = %d, want %d", w.Code, http.StatusSeeOther)
			}
			if got := w.Header().Get("Location"); got != tt.want {
				t.Errorf("SaveWishlist(return=%q) redirects to %q, want %q", tt.ret, got, tt.want)
			}
		})
	}
}
