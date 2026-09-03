package twofactor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// keyBytes is the AES-256 key length GOEN_TOTP_KEY must decode to.
const keyBytes = 32

// ParseKey decodes a configured GOEN_TOTP_KEY into raw key material. An empty
// value yields (nil, nil): a deployment with no key, which newCipher turns into
// a disabled cipher.
//
// It is a key and not a passphrase: an unsalted SHA-256 over a memorable phrase
// is not a key derivation function, and costs an offline attacker one hash per
// wordlist entry against every stored credential at once. Anything that is not
// exactly keyBytes bytes of hex or base64 is refused at startup rather than
// normalised into something that merely looks like a key.
func ParseKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	decoders := []func(string) ([]byte, error){
		hex.DecodeString,
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	}
	decodedLength := -1
	for _, decode := range decoders {
		key, err := decode(value)
		if err != nil {
			continue
		}
		if decodedLength == -1 {
			decodedLength = len(key)
		}
		if len(key) != keyBytes {
			continue
		}
		identical := true
		for _, b := range key[1:] {
			if b != key[0] {
				identical = false
				break
			}
		}
		if identical {
			return nil, errors.New("twofactor: GOEN_TOTP_KEY decoded to 32 identical bytes, " +
				"which is a placeholder rather than a key; generate one with `openssl rand -hex 32`")
		}
		return key, nil
	}
	if decodedLength >= 0 {
		return nil, fmt.Errorf("twofactor: GOEN_TOTP_KEY must be %d random bytes as hex or base64 — "+
			"generate one with `openssl rand -hex 32`; the configured value decoded to %d bytes",
			keyBytes, decodedLength)
	}
	return nil, errors.New("twofactor: GOEN_TOTP_KEY is neither hex nor base64 — it is a key, " +
		"not a passphrase; generate one with `openssl rand -hex 32`")
}

// secretCipher encrypts TOTP secrets at rest. The zero value refuses everything,
// which is what a deployment with no key gets.
type secretCipher struct {
	aead cipher.AEAD
}

// newCipher builds a secretCipher from key material produced by ParseKey. A nil
// or empty key yields a disabled cipher rather than an error.
func newCipher(key []byte) *secretCipher {
	if len(key) == 0 {
		return &secretCipher{}
	}
	if len(key) != keyBytes {
		// Unreachable: ParseKey is the only configuration door, and runs at startup.
		panic("twofactor: newCipher needs a key from ParseKey")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		// Unreachable: a 32-byte key is always a valid AES key size.
		panic("twofactor: aes: " + err.Error())
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("twofactor: gcm: " + err.Error())
	}
	return &secretCipher{aead: aead}
}

func (c *secretCipher) enabled() bool { return c.aead != nil }

// seal encrypts a secret for storage. The nonce is random per call and never
// derived from anything that could repeat: GCM under nonce reuse leaks the XOR
// of the two plaintexts and the authentication key.
func (c *secretCipher) seal(secret []byte) ([]byte, error) {
	if !c.enabled() {
		return nil, ErrDisabled
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, secret, nil), nil
}

// open decrypts a stored secret. A failure is an operational problem — the key
// changed, or the row was tampered with — never a wrong code.
func (c *secretCipher) open(sealed []byte) ([]byte, error) {
	if !c.enabled() {
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
