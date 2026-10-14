// Package bearer makes and digests goen's opaque bearer tokens: the session
// and cart cookies, the order-access cookie, and the tokens in reset,
// verification and newsletter links. Whoever holds a token holds what it
// grants, so the database keeps only its digest.
//
// This is the only place a token is generated or digested. The encoding is a
// stored contract: New returns 32 bytes from crypto/rand as unpadded
// base64url (43 characters), and Digest is the SHA-256 of the token's text.
// Six tables store digests made this way, so neither may ever change.
package bearer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

// New returns a fresh token.
func New() string {
	b := make([]byte, tokenBytes)
	// Since Go 1.24 Read never returns an error and always fills b.
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Digest returns the 32-byte SHA-256 of token's text, the form the database stores.
func Digest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
