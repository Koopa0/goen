// Package email holds goen's policy for an email address.
//
// The parsing is net/mail's, not ours. What lives here is the little that the
// standard library does not decide: how an address is normalised before it is
// stored, the length goen accepts, and the rule that a field collecting an
// address rejects a display name. The contact form and the newsletter signup
// would otherwise each carry their own copy and drift apart.
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

// Valid reports whether s is a bare, storable address. mail.ParseAddress also
// accepts a display name such as `王小明 <a@example.com>`, which no goen field
// collects, so the parsed address must account for the whole input.
//
// It needs no control-character check of its own: mail.ParseAddress refuses
// them, in a quoted local part as well as outside one. A loop added here on
// the assumption that it did not was unreachable, and a mutation run is what
// showed it — removing it changed no test.
func Valid(s string) bool {
	if s == "" || len(s) > Max {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}
