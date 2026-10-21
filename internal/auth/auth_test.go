package auth

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
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
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
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
// The closed pool makes the ordering deterministic without a timing assertion.
func TestAnOverLongPasswordIsRefusedBeforeTheRead(t *testing.T) {
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
		store: store, log: slog.New(slog.DiscardHandler), google: &google.Client{},
		signinLimit: accountTestLimiter(), guessCeiling: accountTestLimiter(),
		resetLimit: accountTestLimiter(),
	}
}

// TestRegistrationsOfOneAddressAreBounded: a registration mails the address it
// names, whether or not that address has an account, so the form must not be a
// way to fill one inbox. The bound is keyed on the address as the rules read it
// and refuses every address the same way.
func TestRegistrationsOfOneAddressAreBounded(t *testing.T) {
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

// TestSignInKeepsItsAccountFallback locks the caller's choice. siteurl.Path
// owns what is safe; account owns where a refused post-sign-in redirect lands.
func TestSignInKeepsItsAccountFallback(t *testing.T) {
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

func TestNormaliseGoogleIdentityBoundsProviderData(t *testing.T) {
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

func TestUserAgentDecorationIsBoundedWithoutRejectingTheSession(t *testing.T) {
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

// TestNoFieldMessageLeaksAFormatVerb asserts the RENDERED string rather than the
// catalogue: only the too-short message carries a format verb, and its refusal
// carries the minimum, so no message renders "%!d(MISSING)" or an unfilled verb
// beside the field.
func TestNoFieldMessageLeaksAFormatVerb(t *testing.T) {
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

func TestAccountNoticeExplainsWhyAnOpenReturnBlocksErasure(t *testing.T) {
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

func TestAccountNoticeExplainsWhyTheLastAdminCannotBeErased(t *testing.T) {
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

func TestCartRecoveryLandingPreservesContinuation(t *testing.T) {
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

func TestAdjustedCartLandingKeepsContinuation(t *testing.T) {
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

func TestConfirmationAcknowledgementsAreSafeToRefresh(t *testing.T) {
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
			const token = "private-confirmation-token"
			const address = "private-mailbox@example.com"
			var first string
			for attempt := range 2 {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet,
					tt.path+"?done=1&token="+token+"&email="+address, http.NoBody)
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
				for _, forbidden := range []string{token, address, `name="token"`, `class="notice__form"`, "%s", "%!("} {
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
					tt.path+"?token="+token+"&done="+marker, http.NoBody)
				res := httptest.NewRecorder()
				tt.handler(res, req)
				if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `name="token" value="`+token+`"`) ||
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

func TestInvalidVerificationOffersEmailResend(t *testing.T) {
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

func TestVerificationInfrastructureFailuresKeepTheirOwnState(t *testing.T) {
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

func TestRegistrationLandingKeepsTheNextStepAfterWelcome(t *testing.T) {
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

// TestPasswordCostIsOWASPSecondOption locks the work factor of every stored
// password hash. HashPassword reads passwordCost, which is declared from these
// constants and which only export_test.go may lower, so a change here changes
// what production spends per sign-in and what an attacker must spend per guess.
func TestPasswordCostIsOWASPSecondOption(t *testing.T) {
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
	fset := gotoken.NewFileSet()
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
				if n.Op == gotoken.AND {
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

func assertPasswordCostDeclaredFromConstants(t *testing.T, fset *gotoken.FileSet, spec *ast.ValueSpec) {
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

// TestSignInMissesAreChargedPerNetworkAndInTotal: a miss is any password the
// store refuses before it reads the database, so this runs on a dead pool.
// The integration tests prove the right password still gets in.
func TestSignInMissesAreChargedPerNetworkAndInTotal(t *testing.T) {
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
