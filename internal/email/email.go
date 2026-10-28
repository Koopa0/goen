// Package email holds the shop's policy for an address: how one is normalised
// before it is stored, the length goen accepts, the rule that a field
// collecting an address rejects a display name, and the masked form a page may
// show. Every feature that collects an address and every send checks it. The
// parsing is net/mail's.
//
// It imports no other goen package. It does not hold the letters goen mails:
// internal/letter composes and sends them, and internal/outbox queues them.
package email

import (
	"net/mail"
	"strings"
)

// MaxBytes is the longest address permitted, in bytes, from the SMTP
// forward-path limit in RFC 5321 §4.5.3.1.3.
const MaxBytes = 254

// Clean trims surrounding whitespace and folds the address to lower case,
// which is the form goen stores and compares.
func Clean(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Valid reports whether s is a bare, storable address: mail.ParseAddress accepts
// a display name, so the parsed address must account for the whole input. It
// already refuses control characters, quoted local part included.
//
// The domain must carry a dot, which RFC 5322 does not require — user@localhost
// is legal and this refuses it, because a shop posting to the public internet
// cannot reach a dotless domain.
func Valid(s string) bool {
	if s == "" || len(s) > MaxBytes {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	domain := s[at+1:]
	dot := strings.IndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1
}
