// Package account is the customer's own pages: the overview with its orders,
// membership standing and store credit, the profile, the address book and the
// wishlist. How a customer proves who they are, and every change to that proof,
// is package auth.
package account

import (
	"errors"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound     = errors.New("account: not found")
	ErrInvalidInput = errors.New("account: invalid input")
)

// These mirror the account form's maxlength attributes; the server owns the
// invariant because a raw HTTP client never sees those hints.
const (
	maxPhoneRunes        = 30
	maxAddressLabelRunes = 30
)

func profileInputValid(name, phone string) bool {
	return utf8.RuneCountInString(name) <= user.MaxNameRunes &&
		utf8.RuneCountInString(phone) <= maxPhoneRunes &&
		!web.HasControlChars(name) && !web.HasControlChars(phone)
}
