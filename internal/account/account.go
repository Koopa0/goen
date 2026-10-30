// Package account is the customer's own pages: the overview with its orders,
// membership standing and store credit, the profile and the address book. How
// a customer proves who they are, and every change to that proof, is package
// auth; the products they saved to buy later are package wishlist.
package account

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/fieldrule"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
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

// normalizeProfile is the profile as it is kept: trimmed, with the phone
// folded to the width the delivery forms keep.
func normalizeProfile(name, phone string) (cleanName, cleanPhone string) {
	return strings.TrimSpace(name), strings.TrimSpace(formtext.FoldWidth(phone))
}

// profileRefusals holds a normalized profile to the form's own bounds. A phone
// that is too long reads as not being one, as it does on the delivery forms.
func profileRefusals(name, phone string) []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	if utf8.RuneCountInString(name) > user.MaxNameRunes {
		errs = append(errs, formtext.FieldRefusal{Field: "name", MessageKey: i18n.KeyNameTooLong})
	}
	if formtext.HasControlChars(name) {
		errs = append(errs, formtext.FieldRefusal{Field: "name", MessageKey: i18n.KeyFieldHasControlChars})
	}
	if utf8.RuneCountInString(phone) > maxPhoneRunes {
		errs = append(errs, formtext.FieldRefusal{Field: "phone", MessageKey: fieldrule.Phone.Message})
	}
	if formtext.HasControlChars(phone) {
		errs = append(errs, formtext.FieldRefusal{Field: "phone", MessageKey: i18n.KeyFieldHasControlChars})
	}
	return errs
}
