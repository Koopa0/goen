// Package email holds goen's policy for an email address: how one is normalised
// before it is stored, the length goen accepts, and the rule that a field
// collecting an address rejects a display name. The parsing is net/mail's.
package email

import (
	"net/mail"
	"strings"
)

// Max is the longest address permitted, from the SMTP forward-path limit in
// RFC 5321 §4.5.3.1.3.
const Max = 254

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
// is a legal address and this refuses it. A shop that posts to the public
// internet cannot reach a dotless domain, so accepting one only delays the
// refusal until the customer is waiting for mail that cannot arrive.
func Valid(s string) bool {
	if s == "" || len(s) > Max {
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
