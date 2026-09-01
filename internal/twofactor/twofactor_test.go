package twofactor

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// rfcSecret is the shared secret from RFC 6238's test vectors.
var rfcSecret = []byte("12345678901234567890")

const testHexKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

var (
	testCipherKey      = []byte("0123456789abcdef0123456789abcdef")
	differentCipherKey = []byte("fedcba9876543210fedcba9876543210")
)

func TestEveryAcceptedStaffRoleHasALabel(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, role := range roles {
		if label := roleLabel(ctx, role); label == "" || label == role {
			t.Errorf("roleLabel(%q) = %q, want a catalogue label", role, label)
		}
	}
}

func mustParseKey(t *testing.T, value string) []byte {
	t.Helper()
	key, err := ParseKey(value)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return key
}

// TestOnlyThirtyTwoRandomBytesIsAKey pins the configuration grammar and the
// decoded material. An err-only assertion would admit the old SHA-256
// normaliser, which accepted every passphrase and returned a plausible length.
func TestOnlyThirtyTwoRandomBytesIsAKey(t *testing.T) {
	hexBytes, err := hex.DecodeString(testHexKey)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	urlBytes := append([]byte{0xfb, 0xff}, hexBytes[:30]...)
	tests := []struct {
		name  string
		input string
		want  []byte
	}{
		{name: "hex", input: testHexKey, want: hexBytes},
		{name: "upper hex", input: strings.ToUpper(testHexKey), want: hexBytes},
		{name: "trimmed hex", input: "  " + testHexKey + "\n", want: hexBytes},
		{name: "standard base64", input: base64.StdEncoding.EncodeToString(hexBytes), want: hexBytes},
		{name: "raw standard base64", input: base64.RawStdEncoding.EncodeToString(hexBytes), want: hexBytes},
		{name: "raw URL base64", input: base64.RawURLEncoding.EncodeToString(urlBytes), want: urlBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, parseErr := ParseKey(tt.input)
			if parseErr != nil {
				t.Fatalf("ParseKey: %v", parseErr)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("decoded key = %x, want %x", got, tt.want)
			}
		})
	}

	for _, input := range []string{
		"a-key-from-the-environment",
		"correct horse battery staple",
		"replace_me",
		strings.Repeat("01", 31),
		strings.Repeat("01", 33),
		strings.Repeat("0", 64),
		strings.Repeat("*", 64),
	} {
		t.Run("refuse "+input[:min(12, len(input))], func(t *testing.T) {
			got, parseErr := ParseKey(input)
			if parseErr == nil {
				t.Fatalf("ParseKey accepted %q as %x", input, got)
			}
			if strings.Contains(parseErr.Error(), input) {
				t.Errorf("startup error leaked the configured key: %v", parseErr)
			}
		})
	}

	key, err := ParseKey("")
	if err != nil || key != nil {
		t.Errorf("ParseKey(empty) = %x, %v; want nil, nil", key, err)
	}
	if newCipher(nil).enabled() {
		t.Error("a nil key enabled the cipher")
	}
}

// FuzzParseKey keeps arbitrary configuration text on the parser boundary and
// pins the useful success invariants: one exact AES-256 key, stable across both
// supported encodings, or no key at all for whitespace-only input.
func FuzzParseKey(f *testing.F) {
	for _, seed := range []string{
		"", " \t\n", testHexKey, strings.ToUpper(testHexKey),
		base64.StdEncoding.EncodeToString(testCipherKey),
		base64.RawURLEncoding.EncodeToString(testCipherKey),
		"correct horse battery staple", strings.Repeat("0", 64), "%%%",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		key, err := ParseKey(value)
		if err != nil {
			if key != nil {
				t.Errorf("ParseKey(%q) returned key %x with error %v", value, key, err)
			}
			return
		}
		if strings.TrimSpace(value) == "" {
			if key != nil {
				t.Errorf("ParseKey(%q) = %x, want nil key", value, key)
			}
			return
		}
		if len(key) != 32 {
			t.Fatalf("ParseKey(%q) returned %d bytes, want 32", value, len(key))
		}

		for _, encoded := range []string{
			hex.EncodeToString(key),
			base64.RawURLEncoding.EncodeToString(key),
		} {
			roundTrip, roundTripErr := ParseKey(encoded)
			if roundTripErr != nil {
				t.Fatalf("ParseKey(%q) round trip: %v", value, roundTripErr)
			}
			if !bytes.Equal(roundTrip, key) {
				t.Errorf("ParseKey(%q) round trip = %x, want %x", value, roundTrip, key)
			}
		}
	})
}

// TestTheKeyIsTheDecodedBytesAndNotAHashOfThem names the defect: the raw
// decoded bytes open the ciphertext and sha256(the environment string) does
// not.
func TestTheKeyIsTheDecodedBytesAndNotAHashOfThem(t *testing.T) {
	decoded := mustParseKey(t, testHexKey)
	sealed, err := newCipher(decoded).seal([]byte("the stored totp secret"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	openWith := func(key []byte) *secretCipher {
		block, blockErr := aes.NewCipher(key)
		if blockErr != nil {
			t.Fatalf("aes: %v", blockErr)
		}
		aead, gcmErr := cipher.NewGCM(block)
		if gcmErr != nil {
			t.Fatalf("gcm: %v", gcmErr)
		}
		return &secretCipher{aead: aead}
	}
	if _, err := openWith(decoded).open(sealed); err != nil {
		t.Errorf("decoded bytes did not open their ciphertext: %v", err)
	}
	hashed := sha256.Sum256([]byte(testHexKey))
	if _, err := openWith(hashed[:]).open(sealed); err == nil {
		t.Error("sha256(the configured text) still opens the ciphertext")
	}
}

// TestAnUnreadableCredentialNoticeSpeaksBothLocales pins the recovery message
// used by the Verify redirect. Challenge's no-redirect path is exercised by
// the integration test against a real unreadable row.
func TestAnUnreadableCredentialNoticeSpeaksBothLocales(t *testing.T) {
	tests := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "這組驗證器已無法讀取"},
		{i18n.En, "This authenticator can no longer be read"},
	}
	for _, tt := range tests {
		r, err := url.Parse("/admin/verify?stale=1")
		if err != nil {
			t.Fatalf("request URL: %v", err)
		}
		req := &http.Request{URL: r}
		req = req.WithContext(i18n.WithLocale(t.Context(), tt.locale))
		if got := noticeFor(req); !strings.Contains(got, tt.want) {
			t.Errorf("notice in %s = %q, want %q", tt.locale, got, tt.want)
		}
	}
}

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
	c := newCipher(testCipherKey)
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	sealed, err := c.seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("the plaintext secret appears in the stored bytes")
	}

	opened, err := c.open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, secret) {
		t.Error("the secret did not round-trip")
	}

	// Equal ciphertexts would mean a reused nonce, which in GCM leaks the XOR
	// of the two plaintexts and the authentication key.
	again, err := c.seal(secret)
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
	c := newCipher(testCipherKey)
	secret, _ := NewSecret()
	sealed, err := c.seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	for _, flip := range []int{0, len(sealed) / 2, len(sealed) - 1} {
		tampered := bytes.Clone(sealed)
		tampered[flip] ^= 0x01
		if _, err := c.open(tampered); err == nil {
			t.Errorf("a ciphertext with byte %d flipped still opened", flip)
		}
	}

	if _, err := newCipher(differentCipherKey).open(sealed); err == nil {
		t.Error("a secret opened under the wrong key")
	}
	// A truncated value is refused rather than panicking on a short slice.
	if _, err := c.open(sealed[:4]); err == nil {
		t.Error("a truncated ciphertext opened")
	}
}

// TestNoKeyMeansNoStoredSecret proves an unconfigured deployment refuses
// enrolment rather than storing the secret in the clear.
func TestNoKeyMeansNoStoredSecret(t *testing.T) {
	c := newCipher(nil)
	if c.enabled() {
		t.Fatal("a cipher with no key reported itself enabled")
	}
	if _, err := c.seal([]byte("secret")); err == nil {
		t.Error("a secret was sealed with no key configured")
	}
	if _, err := c.open([]byte("anything")); err == nil {
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
