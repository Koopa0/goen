package twofactor

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const keyBytes = 32

// A stored secret is header ‖ nonce ‖ ciphertext‖tag.
const (
	// formatV1 is the first byte of every stored secret. A reader that meets
	// another value knows it cannot interpret the rest, instead of guessing and
	// reporting an authentication failure. Rows written before the header existed
	// start with a random nonce byte, so they are told apart by the key id and the
	// tag, not by this byte alone.
	formatV1 byte = 1
	// keyIDBytes is the length of the key identifier that follows the version:
	// the leading bytes of sha256(key). It names which of the two configured keys
	// sealed a row so a rotation can read both; four bytes tell two keys apart,
	// and it authenticates nothing, which the AEAD does.
	keyIDBytes = 4
	// headerBytes is the version byte plus the key id. The whole header is bound
	// into the AAD, so editing it makes the row fail to open.
	headerBytes = 1 + keyIDBytes
)

var (
	// errOldFormat is a stored value this reader cannot parse: written before
	// format v1, or cut short.
	errOldFormat = errors.New("twofactor: the secret was stored before format v1; remove the factor and re-enrol")
	// errUnknownKey is a v1 value whose key id matches neither configured key.
	errUnknownKey = errors.New("twofactor: the secret was sealed under a key this server does not hold")
	// errNotAuthentic is a value that names a key we hold but does not
	// authenticate: another owner's row, or tampering.
	errNotAuthentic = errors.New("twofactor: stored secret does not authenticate")
)

// ParseKey decodes a configured TOTP key into raw key material. An empty
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
			return nil, errors.New("twofactor: the key decoded to 32 identical bytes, " +
				"which is a placeholder rather than a key; generate one with `openssl rand -hex 32`")
		}
		return key, nil
	}
	if decodedLength >= 0 {
		return nil, fmt.Errorf("twofactor: the key must be %d random bytes as hex or base64 — "+
			"generate one with `openssl rand -hex 32`; the configured value decoded to %d bytes",
			keyBytes, decodedLength)
	}
	return nil, errors.New("twofactor: the key is neither hex nor base64 — it is a key, " +
		"not a passphrase; generate one with `openssl rand -hex 32`")
}

// keyedAEAD is one configured key: its AEAD and the identifier written into the
// header of everything it seals.
type keyedAEAD struct {
	id   [keyIDBytes]byte
	aead cipher.AEAD
}

func newKeyedAEAD(key []byte) *keyedAEAD {
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
	k := &keyedAEAD{aead: aead}
	digest := sha256.Sum256(key)
	copy(k.id[:], digest[:keyIDBytes])
	return k
}

// CheckKeyRotation refuses a previous key that cannot be told from the current
// one: identical, or sharing a key id, which would make a stored row ambiguous
// between them. It reports nothing for an absent previous key.
func CheckKeyRotation(key, previous []byte) error {
	if len(previous) == 0 {
		return nil
	}
	if bytes.Equal(key, previous) {
		return errors.New("twofactor: the previous key is the same as the current key")
	}
	if newKeyedAEAD(key).id == newKeyedAEAD(previous).id {
		return errors.New("twofactor: the previous key has the same identifier as the current key; generate a different one")
	}
	return nil
}

// secretCipher encrypts TOTP secrets at rest. The zero value refuses everything,
// which is what a deployment with no key gets. It seals under the current key
// and opens under the current key or, during a rotation, the previous one.
type secretCipher struct {
	current  *keyedAEAD
	previous *keyedAEAD
}

// newCipher takes keys from ParseKey. An empty key yields a disabled cipher
// whatever previous is; previous may be empty.
func newCipher(key, previous []byte) *secretCipher {
	if len(key) == 0 {
		return &secretCipher{}
	}
	c := &secretCipher{current: newKeyedAEAD(key)}
	if len(previous) != 0 {
		c.previous = newKeyedAEAD(previous)
		if c.previous.id == c.current.id {
			// Unreachable: CheckKeyRotation refuses it at startup.
			panic("twofactor: the previous key shares the current key's identifier")
		}
	}
	return c
}

func (c *secretCipher) enabled() bool { return c.current != nil }

// aad binds a stored secret to its version, its key and its owner. Without the
// owner, a row copied onto another staff member's opens there as a factor the
// attacker holds.
func aad(header []byte, owner uuid.UUID) []byte {
	return append(bytes.Clone(header), owner[:]...)
}

// seal encrypts a secret for its owner under the current key. The nonce is
// random per call and never derived from anything that could repeat: GCM under
// nonce reuse leaks the XOR of the two plaintexts and the authentication key.
func (c *secretCipher) seal(secret []byte, owner uuid.UUID) ([]byte, error) {
	if !c.enabled() {
		return nil, ErrDisabled
	}
	aead := c.current.aead
	out := make([]byte, headerBytes, headerBytes+aead.NonceSize()+len(secret)+aead.Overhead())
	out[0] = formatV1
	copy(out[1:], c.current.id[:])
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	extra := aad(out[:headerBytes], owner)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, secret, extra), nil
}

// open decrypts a stored secret for its owner. current reports whether the
// current key opened it, so a caller can re-seal what the previous key did. A
// failure is an operational problem — the key changed, the row is from before
// format v1, or it was tampered with or moved — never a wrong code.
func (c *secretCipher) open(sealed []byte, owner uuid.UUID) (secret []byte, current bool, err error) {
	if !c.enabled() {
		return nil, false, ErrDisabled
	}
	aead := c.current.aead
	if len(sealed) < headerBytes+aead.NonceSize()+aead.Overhead() || sealed[0] != formatV1 {
		return nil, false, errOldFormat
	}
	header := sealed[:headerBytes]
	var key *keyedAEAD
	switch id := header[1:]; {
	case bytes.Equal(id, c.current.id[:]):
		key, current = c.current, true
	case c.previous != nil && bytes.Equal(id, c.previous.id[:]):
		key = c.previous
	default:
		return nil, false, errUnknownKey
	}
	nonce := sealed[headerBytes : headerBytes+aead.NonceSize()]
	ciphertext := sealed[headerBytes+aead.NonceSize():]
	secret, err = key.aead.Open(nil, nonce, ciphertext, aad(header, owner))
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", errNotAuthentic, err)
	}
	return secret, current, nil
}
