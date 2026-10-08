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
	"github.com/koopa0/goen/internal/ui/pages/pagestest"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
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
		store: store, log: slog.New(slog.DiscardHandler), google: &Google{},
		signinLimit: accountTestLimiter(), resetLimit: accountTestLimiter(),
	}
}

// TestRegistrationsOfOneAddressAreBounded: a registration mails the address it
// names, whether or not that address has an account, so the form must not be a
// way to fill one inbox. The bound is keyed on the address as the rules read it
// and refuses every address the same way.
func TestRegistrationsOfOneAddressAreBounded(t *testing.T) {
	h := NewHandler(deadAccountStore(t), nil, slog.New(slog.DiscardHandler), false, nil)
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

func TestProfileAndSavedAddressBoundsMatchTheRenderedForms(t *testing.T) {
	t.Parallel()

	if !profileInputValid(strings.Repeat("名", maxNameRunes), strings.Repeat("1", maxPhoneRunes)) {
		t.Fatal("profile validator rejects the exact rendered maxlength")
	}
	for _, tt := range []struct {
		name, phone string
	}{
		{name: strings.Repeat("名", maxNameRunes+1)},
		{phone: strings.Repeat("1", maxPhoneRunes+1)},
		{name: "name\nwith control"},
	} {
		if profileInputValid(tt.name, tt.phone) {
			t.Errorf("profile accepted name=%q phone=%q outside its server bounds", tt.name, tt.phone)
		}
	}

	base := Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	for _, phone := range []string{
		"0912345678",
		"+886 2 2700-1234",
		"(02) 2700-1234",
	} {
		a := base
		a.Phone = phone
		if errs := a.Validate(); len(errs) != 0 {
			t.Errorf("ordinary delivery phone %q was rejected: %+v", phone, errs)
		}
	}
	for _, tt := range []struct {
		name  string
		field string
		mut   func(*Address)
	}{
		{"label", "label", func(a *Address) { a.Label = strings.Repeat("標", maxAddressLabelRunes+1) }},
		{"name", "name", func(a *Address) { a.Name = strings.Repeat("名", maxNameRunes+1) }},
		{"phone", "phone", func(a *Address) { a.Phone = strings.Repeat("1", maxPhoneRunes+1) }},
		{"postal code", "postal_code", func(a *Address) { a.PostalCode = strings.Repeat("1", maxPostalCodeRunes+1) }},
		{"city", "city", func(a *Address) { a.City = strings.Repeat("市", maxCityRunes+1) }},
		{"district", "district", func(a *Address) { a.District = strings.Repeat("區", maxDistrictRunes+1) }},
		{"street", "street", func(a *Address) { a.Street = strings.Repeat("路", maxStreetRunes+1) }},
		{"phone letters", "phone", func(a *Address) { a.Phone = "09AB123456" }},
		{"phone too few digits", "phone", func(a *Address) { a.Phone = "02-12345" }},
		{"phone too many digits", "phone", func(a *Address) { a.Phone = "1234567890123456" }},
		{"phone representation too long", "phone", func(a *Address) {
			a.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes)
		}},
		{"postal code letters", "postal_code", func(a *Address) { a.PostalCode = "11A" }},
		{"postal code too short", "postal_code", func(a *Address) { a.PostalCode = "11" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := base
			tt.mut(&a)
			errs := a.Validate()
			for _, err := range errs {
				if err.Field == tt.field {
					return
				}
			}
			t.Errorf("overlong %s was accepted: %+v", tt.field, errs)
		})
	}
}

func TestNormaliseGoogleIdentityBoundsProviderData(t *testing.T) {
	t.Parallel()

	valid := Identity{
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

	for _, id := range []Identity{
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

	valid.Name = strings.Repeat("名", maxNameRunes+1)
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
// catalogue: only the too-short message carries a format verb, and formatting
// the other six would render "%!(EXTRA int=10)" beside the field.
func TestNoFieldMessageLeaksAFormatVerb(t *testing.T) {
	t.Parallel()

	every := []web.FieldRefusal{
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
		for field, msg := range FieldMessages(ctx, every) {
			if strings.Contains(msg, "%!") || strings.Contains(msg, "%d") ||
				strings.Contains(msg, "%s") {
				t.Errorf("%s: the %s field renders %q — a format verb reached the "+
					"customer", locale, field, msg)
			}
		}
	}

	// The one message that does carry a verb still gets its number.
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	short := FieldMessages(ctx, []web.FieldRefusal{
		{Field: "password", MessageKey: i18n.KeyPasswordTooShort},
	})["password"]
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
		if got, want := accountNotice(r), i18n.T(ctx, i18n.KeyEraseOpenReturn); got != want {
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
		if got, want := accountNotice(r), i18n.T(ctx, i18n.KeyEraseLastAdmin); got != want {
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

// TestAGuestSavingIsSentBackToTheProduct holds where a refused save lands: the
// form's validated same-site return path, never a fixed /account/wishlist.
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

// TestAccountOrderPageRedirectsToCanonical holds that the legacy account order
// URL forwards to /orders/{number}, where shipment, return and funding live.
func TestAccountOrderPageRedirectsToCanonical(t *testing.T) {
	t.Parallel()

	h := &Handler{log: slog.New(slog.DiscardHandler)}
	ctx := user.NewContext(t.Context(), user.User{ID: "11110000-0000-4000-8000-000000000001"})
	r := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/account/orders/GO-260101-000012", http.NoBody)
	r.SetPathValue("number", "GO-260101-000012")
	w := httptest.NewRecorder()

	h.OrderPage(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("OrderPage status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/orders/GO-260101-000012" {
		t.Errorf("OrderPage redirects to %q, want /orders/GO-260101-000012", got)
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

func TestSavedAddressFoldsFullWidthDigits(t *testing.T) {
	a := Address{Name: "王小明", Phone: "０９１２３４５６７８", PostalCode: "１１０", City: "台北市", District: "信義區", Street: "市府路1號"}
	a.Trim()
	if a.Phone != "0912345678" || a.PostalCode != "110" {
		t.Fatalf("Trim kept phone %q postal code %q, want the ASCII forms", a.Phone, a.PostalCode)
	}
	for _, e := range a.Validate() {
		if e.Field == "phone" || e.Field == "postal_code" {
			t.Errorf("full-width input refused: %s %v", e.Field, e.MessageKey)
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
		{name: "verification-En", handler: h.VerifyPage, path: "/verify", locale: i18n.En, heading: "Address confirmed", body: "Your email address is confirmed. Everything we send you goes there from now on."},
		{name: "verification-ZhHant", handler: h.VerifyPage, path: "/verify", locale: i18n.ZhHant, heading: "\u4fe1\u7bb1\u5df2\u78ba\u8a8d", body: "\u96fb\u5b50\u90f5\u4ef6\u5df2\u78ba\u8a8d\uff0c\u4e4b\u5f8c\u7684\u901a\u77e5\u4fe1\u90fd\u6703\u5bc4\u5230\u9019\u500b\u4fe1\u7bb1\u3002"},
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
			pagestest.AssertEmailLink(t, res.Body.String(), heading, reason, "/account#email-heading")
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
