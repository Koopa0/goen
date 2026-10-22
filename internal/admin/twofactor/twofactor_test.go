package twofactor

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// rfcSecret is the shared secret from RFC 6238's test vectors.
var rfcSecret = []byte("12345678901234567890")

const testHexKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

var (
	testCipherKey      = []byte("0123456789abcdef0123456789abcdef")
	differentCipherKey = []byte("fedcba9876543210fedcba9876543210")
)

func mustParseKey(t *testing.T, value string) []byte {
	t.Helper()
	key, err := ParseKey(value)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	return key
}

// TestParseKey: only thirty-two random bytes is a key. It pins the
// configuration grammar and the decoded material: an err-only assertion would
// admit a normaliser that accepted every passphrase and returned a plausible
// length.
func TestParseKey(t *testing.T) {
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
	if newCipher(nil, nil).enabled() {
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

// TestSecretCipher_SealKeyIsDecodedBytes: the key is the decoded bytes and not
// a hash of them. The raw decoded bytes are the AES key, and sha256(the
// environment string) is not.
func TestSecretCipher_SealKeyIsDecodedBytes(t *testing.T) {
	decoded := mustParseKey(t, testHexKey)
	owner := uuid.New()
	sealed, err := newCipher(decoded, nil).seal([]byte("the stored totp secret"), owner)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	openRaw := func(key []byte) error {
		block, blockErr := aes.NewCipher(key)
		if blockErr != nil {
			t.Fatalf("aes: %v", blockErr)
		}
		aead, gcmErr := cipher.NewGCM(block)
		if gcmErr != nil {
			t.Fatalf("gcm: %v", gcmErr)
		}
		header := sealed[:headerBytes]
		nonce, ciphertext := sealed[headerBytes:headerBytes+aead.NonceSize()], sealed[headerBytes+aead.NonceSize():]
		_, openErr := aead.Open(nil, nonce, ciphertext, aad(header, owner))
		return openErr
	}
	if err := openRaw(decoded); err != nil {
		t.Errorf("decoded bytes did not open their ciphertext: %v", err)
	}
	hashed := sha256.Sum256([]byte(testHexKey))
	if err := openRaw(hashed[:]); err == nil {
		t.Error("sha256(the configured text) still opens the ciphertext")
	}
}

// TestNoticeForUnreadable: an unreadable-credential notice speaks both locales.
// It pins the recovery message used by the Verify redirect.
func TestNoticeForUnreadable(t *testing.T) {
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

// TestCodeRFCVectors: Code matches the RFC 6238 test vectors. It proves goen
// agrees with every authenticator app. RFC 6238's table is 8-digit; goen uses
// the low six, per RFC 4226.
func TestCodeRFCVectors(t *testing.T) {
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

// TestVerifyReplay: a code cannot be used twice. Verify refuses a code at or
// before the last accepted step, so it cannot be replayed for the rest of its
// window, and refuses a code from an earlier step after a later one.
func TestVerifyReplay(t *testing.T) {
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

// TestVerifySkewWindow: the skew window is exactly one step. Verify accepts the
// current step and one step either side, and refuses two or more steps away.
func TestVerifySkewWindow(t *testing.T) {
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

// TestVerifyCodeFormat: nothing but a six-digit code is accepted. Verify
// refuses codes of the wrong length or characters, and accepts a correct code
// with the spacing an authenticator app displays.
func TestVerifyCodeFormat(t *testing.T) {
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

// TestSecretCipher_SealRoundTrip: a sealed secret round-trips and is not the
// plaintext. The stored bytes do not contain the secret, carry the version and
// key-id header, open to the secret under the current key, and differ on every
// seal, so no nonce is reused.
func TestSecretCipher_SealRoundTrip(t *testing.T) {
	c := newCipher(testCipherKey, nil)
	owner := uuid.New()
	secret, err := NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	sealed, err := c.seal(secret, owner)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("the plaintext secret appears in the stored bytes")
	}
	digest := sha256.Sum256(testCipherKey)
	wantHeader := append([]byte{0x01}, digest[:4]...)
	if !bytes.Equal(sealed[:headerBytes], wantHeader) {
		t.Errorf("header = %x, want version 01 then the key id: %x", sealed[:headerBytes], wantHeader)
	}

	opened, current, err := c.open(sealed, owner)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, secret) {
		t.Error("the secret did not round-trip")
	}
	if !current {
		t.Error("the current key opened its own seal but reported current = false")
	}

	// Equal ciphertexts would mean a reused nonce, which in GCM leaks the XOR
	// of the two plaintexts and the authentication key.
	again, err := c.seal(secret, owner)
	if err != nil {
		t.Fatalf("seal again: %v", err)
	}
	if bytes.Equal(sealed, again) {
		t.Error("two seals produced identical bytes; the nonce is being reused")
	}
}

// TestSecretCipher_OpenOtherOwner: a secret sealed for one owner does not open
// for another. The owner is in the AAD, so a row copied onto another staff
// member's does not open there.
func TestSecretCipher_OpenOtherOwner(t *testing.T) {
	c := newCipher(testCipherKey, nil)
	a, b := uuid.New(), uuid.New()
	sealed, err := c.seal(rfcSecret, a)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, openErr := c.open(sealed, a); openErr != nil {
		t.Fatalf("open for its own owner: %v", openErr)
	}
	_, _, err = c.open(sealed, b)
	if !errors.Is(err, errNotAuthentic) {
		t.Fatalf("open for another owner = %v, want %v", err, errNotAuthentic)
	}
}

// TestSecretCipher_KeyRotation: a rotation opens the old key and seals under
// the new one. It walks the key rotation: K1 seals, then K2 with K1 as previous
// opens it and says it is not current, K2 alone refuses it, and new seals carry
// K2's identifier.
func TestSecretCipher_KeyRotation(t *testing.T) {
	k1, k2 := testCipherKey, differentCipherKey
	owner := uuid.New()
	sealedUnderK1, err := newCipher(k1, nil).seal(rfcSecret, owner)
	if err != nil {
		t.Fatalf("seal under k1: %v", err)
	}

	rotating := newCipher(k2, k1)
	secret, current, err := rotating.open(sealedUnderK1, owner)
	if err != nil {
		t.Fatalf("open k1 row under (k2, k1): %v", err)
	}
	if !bytes.Equal(secret, rfcSecret) {
		t.Error("the secret changed across a rotation")
	}
	if current {
		t.Error("a row sealed under the previous key reported current = true")
	}

	_, _, err = newCipher(k2, nil).open(sealedUnderK1, owner)
	if !errors.Is(err, errUnknownKey) || !strings.Contains(err.Error(), "does not hold") {
		t.Errorf("open k1 row under k2 alone = %v, want a key this server does not hold", err)
	}

	resealed, err := rotating.seal(secret, owner)
	if err != nil {
		t.Fatalf("seal under (k2, k1): %v", err)
	}
	k2Digest := sha256.Sum256(k2)
	if !bytes.Equal(resealed[1:headerBytes], k2Digest[:keyIDBytes]) {
		t.Errorf("key id = %x, want the current key's %x", resealed[1:headerBytes], k2Digest[:keyIDBytes])
	}
	if _, current, err := newCipher(k2, nil).open(resealed, owner); err != nil || !current {
		t.Errorf("k2 alone opening the re-sealed row = current %v, %v; want true, nil", current, err)
	}
}

// TestSecretCipher_OpenOldFormat: an old-format secret is refused with the
// recovery advice. A value from before the header existed (nonce ‖ ciphertext
// under the same key) is told to re-enrol.
func TestSecretCipher_OpenOldFormat(t *testing.T) {
	block, err := aes.NewCipher(testCipherKey)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	// A nonce whose first byte is not the version, so the value is refused for
	// its version and not, one run in 256, for its key id.
	nonce := make([]byte, aead.NonceSize())
	for first := true; first || nonce[0] == formatV1; first = false {
		if _, readErr := rand.Read(nonce); readErr != nil {
			t.Fatalf("nonce: %v", readErr)
		}
	}
	v0 := aead.Seal(nonce, nonce, rfcSecret, nil)

	_, _, err = newCipher(testCipherKey, nil).open(v0, uuid.New())
	if !errors.Is(err, errOldFormat) || !strings.Contains(err.Error(), "stored before format v1; remove the factor and re-enrol") {
		t.Errorf("open of a pre-v1 value = %v, want the stored-before-format-v1 error", err)
	}
	// A value too short to hold a header, a nonce and a tag is the same case.
	if _, _, err := newCipher(testCipherKey, nil).open(v0[:headerBytes+4], uuid.New()); !errors.Is(err, errOldFormat) {
		t.Errorf("open of a truncated value = %v, want %v", err, errOldFormat)
	}
}

// TestSecretCipher_OpenTampered: a tampered secret does not open. Flipping a
// byte of the version, key id, nonce, ciphertext or tag, opening under the
// wrong key, and opening a truncated value are all refused, the last without
// panicking.
func TestSecretCipher_OpenTampered(t *testing.T) {
	c := newCipher(testCipherKey, nil)
	owner := uuid.New()
	secret, _ := NewSecret()
	sealed, err := c.seal(secret, owner)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Byte 0 is the version, 1-4 the key id, then the nonce, the ciphertext and
	// the tag. Every one is covered by the version check, the key lookup or the
	// AEAD.
	for _, flip := range []int{0, 1, headerBytes - 1, headerBytes, len(sealed) / 2, len(sealed) - 1} {
		tampered := bytes.Clone(sealed)
		tampered[flip] ^= 0x01
		if _, _, err := c.open(tampered, owner); err == nil {
			t.Errorf("a ciphertext with byte %d flipped still opened", flip)
		}
	}

	if _, _, err := newCipher(differentCipherKey, nil).open(sealed, owner); err == nil {
		t.Error("a secret opened under the wrong key")
	}
	// A truncated value is refused rather than panicking on a short slice.
	if _, _, err := c.open(sealed[:4], owner); err == nil {
		t.Error("a truncated ciphertext opened")
	}
}

// TestCheckKeyRotation: a previous key the key id cannot tell from the current
// one is refused. The rotation check rejects an identical previous key, and
// accepts none or a different one.
func TestCheckKeyRotation(t *testing.T) {
	if err := CheckKeyRotation(testCipherKey, nil); err != nil {
		t.Errorf("no previous key refused: %v", err)
	}
	if err := CheckKeyRotation(testCipherKey, differentCipherKey); err != nil {
		t.Errorf("a different previous key refused: %v", err)
	}
	if err := CheckKeyRotation(testCipherKey, bytes.Clone(testCipherKey)); err == nil {
		t.Error("the same key as its own predecessor was accepted")
	}
}

// TestSecretCipher_NoKey: no key means no stored secret. A cipher without a key
// is not enabled, and neither seals nor opens.
func TestSecretCipher_NoKey(t *testing.T) {
	c := newCipher(nil, nil)
	if c.enabled() {
		t.Fatal("a cipher with no key reported itself enabled")
	}
	if _, err := c.seal([]byte("secret"), uuid.New()); err == nil {
		t.Error("a secret was sealed with no key configured")
	}
	if _, _, err := c.open([]byte("anything"), uuid.New()); err == nil {
		t.Error("a secret was opened with no key configured")
	}
}

// TestProvisioningURI: the provisioning URI is what an authenticator app
// expects. It is otpauth://totp with the issuer, digits, period and algorithm,
// carries the secret as unpadded base32, and an account name with
// metacharacters cannot break it.
func TestProvisioningURI(t *testing.T) {
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

// keylessHandler is a handler over a store with no key, whose every method
// answers ErrDisabled before it reaches the database the pool never connects to.
func keylessHandler(t *testing.T) *Handler {
	t.Helper()
	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	return NewHandler(NewStore(idle, nil, nil), slog.New(slog.DiscardHandler))
}

// TestHandler_EnrolThrottledPerAccount: Enrol is throttled per account. The
// eleventh attempt from one account answers 429 while another account is still
// served.
func TestHandler_EnrolThrottledPerAccount(t *testing.T) {
	t.Parallel()
	h := keylessHandler(t)
	enrol := func(id string) int {
		r := httptest.NewRequestWithContext(user.NewContext(t.Context(), user.User{ID: id, Role: user.RoleStaff}),
			http.MethodPost, "/admin/verify/enrol", strings.NewReader(""))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.Enrol(w, r)
		return w.Code
	}

	for i := range 10 {
		if got := enrol("first"); got != http.StatusSeeOther {
			t.Fatalf("Enrol attempt %d = %d, want 303", i+1, got)
		}
	}
	if got := enrol("first"); got != http.StatusTooManyRequests {
		t.Errorf("Enrol attempt 11 = %d, want 429", got)
	}
	if got := enrol("second"); got != http.StatusSeeOther {
		t.Errorf("Enrol for another account after the first was throttled = %d, want 303", got)
	}
}
