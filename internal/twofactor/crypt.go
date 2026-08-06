package twofactor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Cipher encrypts TOTP secrets at rest.
//
// A TOTP secret is a password-equivalent: whoever holds it can mint valid codes
// forever. staff_totp_credentials.secret_encrypted is named for this reason —
// a database dump alone must not defeat the second factor, so the key lives in
// the environment where a dump does not reach.
//
// The zero value is a Cipher that refuses everything, which is what a
// deployment with no key gets. Enrolment then fails loudly instead of storing a
// secret in the clear.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher derives a Cipher from a configured key.
//
// An empty key yields a disabled Cipher rather than an error: goen runs without
// 2FA the same way it runs without Stripe, and the back office says so instead
// of refusing to start.
//
// The key is hashed rather than used directly, so the configured value may be
// any length — a passphrase, a base64 blob — and still produce the 32 bytes
// AES-256 needs. It is NOT a password hash and does not need to be: the input
// is a machine-generated secret from the environment, not something a person
// chose, so stretching it would cost startup time and buy nothing.
func NewCipher(key string) *Cipher {
	if key == "" {
		return &Cipher{}
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		// Unreachable: a 32-byte key is always a valid AES key size.
		panic("twofactor: aes: " + err.Error())
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("twofactor: gcm: " + err.Error())
	}
	return &Cipher{aead: aead}
}

// Enabled reports whether secrets can be stored.
func (c *Cipher) Enabled() bool { return c.aead != nil }

// Seal encrypts a secret for storage.
//
// The nonce is random per call and prefixed to the ciphertext. GCM is
// catastrophic under nonce reuse — two secrets sealed with the same nonce leak
// their XOR and the authentication key — so it is never derived from anything
// that could repeat, such as the user id.
func (c *Cipher) Seal(secret []byte) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, secret, nil), nil
}

// Open decrypts a stored secret.
//
// A failure here is not "wrong password" — it is a ciphertext that does not
// authenticate, which means the key changed or the row was tampered with. Both
// are operational problems, so the error says so rather than being folded into
// ErrBadCode.
func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if len(sealed) < c.aead.NonceSize() {
		return nil, errors.New("twofactor: stored secret is too short to contain a nonce")
	}
	nonce, ciphertext := sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():]
	secret, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("twofactor: stored secret does not authenticate: %w", err)
	}
	return secret, nil
}
