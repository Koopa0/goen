package account

import (
	"strings"
	"testing"
)

// TestPasswordHashingRoundTrips is the basic contract, plus the two properties
// that make it a password hash rather than a checksum: the same password hashes
// differently every time (a salt), and a wrong password never verifies.
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
	// And the second hash still verifies — a salt that broke verification would
	// pass the check above.
	if !VerifyPassword(other, pw) {
		t.Error("the second hash of the same password does not verify")
	}
}

// TestHashCarriesItsParameters pins that the cost is encoded in the hash. A
// hash without it cannot be re-verified after the parameters change, so raising
// the cost later would mean resetting every password at once.
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

// TestVerifyRejectsMalformedHashes covers what a corrupted or hand-edited
// column would produce. None of it may verify, and none of it may panic.
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
	// Determinism against a value captured first, not against a second call in
	// the same expression — that form is two identical expressions and cannot
	// fail whatever HashToken does.
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

// TestSafeNext is the open-redirect guard. Everything that is not a path on
// this site must fall back, and the protocol-relative forms are the ones a
// naive "starts with /" check lets through.
func TestSafeNext(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ in, want string }{
		{"/account", "/account"},
		{"/account/orders", "/account/orders"},
		{"/cart?x=1", "/cart?x=1"},
		{"", "/account"},
		{"//evil.example", "/account"},        // protocol-relative
		{"/\\evil.example", "/account"},       // backslash form some browsers accept
		{"https://evil.example", "/account"},  // absolute
		{"http://evil.example", "/account"},   // absolute
		{"javascript:alert(1)", "/account"},   // scheme
		{"account", "/account"},               // not rooted
		{"/x\r\nSet-Cookie: a=b", "/account"}, // header injection
		{"/x\nLocation: https://evil", "/account"},
	} {
		if got := SafeNext(tt.in); got != tt.want {
			t.Errorf("SafeNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
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
	// The floor counts RUNES, not bytes. Ten Han characters is a fine password
	// and passes either way, so it proves nothing on its own.
	if PasswordError("密碼密碼密碼密碼密碼") != "" {
		t.Error("a ten-character Chinese password was rejected")
	}
	// This is the case that separates the two: four Han characters is 12 bytes
	// and 4 characters. Counted in bytes it clears a floor of 10 and a weak
	// password gets in; counted in runes it is correctly refused.
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
