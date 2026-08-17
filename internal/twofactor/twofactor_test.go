package twofactor

import (
	"bytes"
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

// rfcSecret is the shared secret from RFC 6238's test vectors.
var rfcSecret = []byte("12345678901234567890")

// TestCodeMatchesTheRFCTestVectors proves goen agrees with every authenticator
// app. RFC 6238's table is 8-digit; goen uses the low six, per RFC 4226.
func TestCodeMatchesTheRFCTestVectors(t *testing.T) {
	tests := []struct {
		unix int64
		want string // the low six digits of the RFC's SHA1 column
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, tt := range tests {
		step := StepAt(time.Unix(tt.unix, 0))
		if got := Code(rfcSecret, step); got != tt.want {
			t.Errorf("Code at %d = %q, want %q", tt.unix, got, tt.want)
		}
	}
}

// TestACodeCannotBeUsedTwice proves a code is spent when it is used.
func TestACodeCannotBeUsedTwice(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	step := StepAt(now)
	code := Code(rfcSecret, step)

	matched, err := Verify(rfcSecret, code, 0, now)
	if err != nil {
		t.Fatalf("a fresh code was refused: %v", err)
	}
	if matched != step {
		t.Errorf("matched step %d, want %d", matched, step)
	}

	if _, err := Verify(rfcSecret, code, matched, now); err == nil {
		t.Error("a code was accepted twice; it can be replayed for the rest of " +
			"its window")
	}

	older := Code(rfcSecret, step-1)
	if _, err := Verify(rfcSecret, older, matched, now); err == nil {
		t.Error("a code from an earlier step was accepted after a later one")
	}
}

// TestTheSkewWindowIsExactlyOneStep proves the acceptance window is as narrow
// as it claims.
func TestTheSkewWindowIsExactlyOneStep(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	current := StepAt(now)

	tests := []struct {
		name   string
		offset int64
		want   bool
	}{
		{"the current step", 0, true},
		{"one step behind", -1, true},
		{"one step ahead", 1, true},
		{"two steps behind", -2, false},
		{"two steps ahead", 2, false},
		{"ten steps behind", -10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := Code(rfcSecret, current+tt.offset)
			_, err := Verify(rfcSecret, code, 0, now)
			if (err == nil) != tt.want {
				t.Errorf("accepted=%v, want %v", err == nil, tt.want)
			}
		})
	}
}

// TestNothingButASixDigitCodeIsAccepted proves malformed input never reaches
// the comparison.
func TestNothingButASixDigitCodeIsAccepted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	valid := Code(rfcSecret, StepAt(now))

	for _, code := range []string{
		"", "12345", "1234567", "abcdef", "12 34 5",
		valid[:5], valid + "0", " " + valid[:5],
	} {
		if _, err := Verify(rfcSecret, code, 0, now); err == nil {
			t.Errorf("%q was accepted", code)
		}
	}

	// Apps display "123 456" and a person types what they see.
	spaced := valid[:3] + " " + valid[3:]
	if _, err := Verify(rfcSecret, "  "+spaced+"  ", 0, now); err != nil {
		t.Errorf("a correct code with the spacing an app displays was refused: %v", err)
	}
}

// TestASealedSecretRoundTripsAndIsNotThePlaintext proves the stored form is
// neither readable nor repeatable.
func TestASealedSecretRoundTripsAndIsNotThePlaintext(t *testing.T) {
	c := NewCipher("a-key-from-the-environment")
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	sealed, err := c.Seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("the plaintext secret appears in the stored bytes")
	}

	opened, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, secret) {
		t.Error("the secret did not round-trip")
	}

	// Equal ciphertexts would mean a reused nonce, which in GCM leaks the XOR
	// of the two plaintexts and the authentication key.
	again, err := c.Seal(secret)
	if err != nil {
		t.Fatalf("seal again: %v", err)
	}
	if bytes.Equal(sealed, again) {
		t.Error("two seals produced identical bytes; the nonce is being reused")
	}
}

// TestATamperedSecretDoesNotOpen proves the encryption authenticates rather
// than merely obscures.
func TestATamperedSecretDoesNotOpen(t *testing.T) {
	c := NewCipher("a-key-from-the-environment")
	secret, _ := NewSecret()
	sealed, err := c.Seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	for _, flip := range []int{0, len(sealed) / 2, len(sealed) - 1} {
		tampered := bytes.Clone(sealed)
		tampered[flip] ^= 0x01
		if _, err := c.Open(tampered); err == nil {
			t.Errorf("a ciphertext with byte %d flipped still opened", flip)
		}
	}

	if _, err := NewCipher("a-different-key").Open(sealed); err == nil {
		t.Error("a secret opened under the wrong key")
	}
	// A truncated value is refused rather than panicking on a short slice.
	if _, err := c.Open(sealed[:4]); err == nil {
		t.Error("a truncated ciphertext opened")
	}
}

// TestNoKeyMeansNoStoredSecret proves an unconfigured deployment refuses
// enrolment rather than storing the secret in the clear.
func TestNoKeyMeansNoStoredSecret(t *testing.T) {
	c := NewCipher("")
	if c.Enabled() {
		t.Fatal("a cipher with no key reported itself enabled")
	}
	if _, err := c.Seal([]byte("secret")); err == nil {
		t.Error("a secret was sealed with no key configured")
	}
	if _, err := c.Open([]byte("anything")); err == nil {
		t.Error("a secret was opened with no key configured")
	}
}

// TestTheProvisioningURIIsWhatAnAppExpects proves an app can import it.
func TestTheProvisioningURIIsWhatAnAppExpects(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := ProvisioningURI("staff@goen.example", secret)

	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("the URI does not parse: %v", err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("scheme/host are %q/%q, want otpauth/totp", u.Scheme, u.Host)
	}

	q := u.Query()
	if q.Get("issuer") != Issuer {
		t.Errorf("issuer is %q", q.Get("issuer"))
	}
	if q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Errorf("parameters are digits=%q period=%q algorithm=%q",
			q.Get("digits"), q.Get("period"), q.Get("algorithm"))
	}

	// Padding is stripped because several apps refuse a secret containing "=".
	encoded := q.Get("secret")
	if strings.Contains(encoded, "=") {
		t.Errorf("the encoded secret %q carries base32 padding", encoded)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(encoded)
	if err != nil {
		t.Fatalf("the encoded secret does not decode: %v", err)
	}
	if !bytes.Equal(decoded, secret) {
		t.Error("the encoded secret is not the one given")
	}

	odd := ProvisioningURI("a?b#c/d@goen.example", secret)
	if _, err := url.Parse(odd); err != nil {
		t.Errorf("an account name with metacharacters broke the URI: %v", err)
	}
	if strings.Count(odd, "?") != 1 {
		t.Errorf("an account name introduced a second query separator: %s", odd)
	}
}
