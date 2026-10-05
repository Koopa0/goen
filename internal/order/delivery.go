package order

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/web"
)

// Delivery is where and to whom an order goes: order_private_data's recipient,
// street address or pickup point, and note.
type Delivery struct {
	// To picks which half is real; it comes from the shipping method, never the
	// form.
	To destination.Kind

	Email         string
	RecipientName string
	Phone         string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     pickup.Chain
	PickupStoreCode string
	PickupStoreName string

	Note string
}

const (
	maxNameRunes       = 60
	maxPhoneRunes      = 30
	maxPostalCodeRunes = 6
	maxCityRunes       = 20
	maxDistrictRunes   = 20
	maxStreetRunes     = 200
	maxStoreNameRunes  = 40
	maxNoteRunes       = 500
	// Every checkout produces an invoice preference. ECPay's Issue contract accepts
	// at most 80 bytes for CustomerEmail; accepting a longer delivery address and
	// truncating it later can turn a valid address into an invalid provider value.
	maxInvoiceEmailBytes = 80
)

func (a *Delivery) Validate() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

	if k := emailError(a.Email); k != "" {
		add("email", k)
	}

	if strings.TrimSpace(a.RecipientName) == "" {
		add("name", i18n.KeyNameRequired)
	} else if utf8.RuneCountInString(a.RecipientName) > maxNameRunes {
		add("name", i18n.KeyNameTooLong)
	}

	switch {
	case strings.TrimSpace(a.Phone) == "":
		add("phone", i18n.KeyPhoneRequired)
	case !looksLikePhone(a.Phone):
		add("phone", i18n.KeyPhoneMalformed)
	}

	errs = append(errs, a.destinationErrors()...)

	if utf8.RuneCountInString(a.Note) > maxNoteRunes {
		add("note", i18n.KeyNoteTooLong)
	}

	return append(errs, a.controlCharErrors()...)
}

func (a *Delivery) destinationErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

	switch a.To {
	case destination.Address:
		switch {
		case strings.TrimSpace(a.PostalCode) == "":
			add("postal_code", i18n.KeyPostalCodeRequired)
		case !isPostalCode(a.PostalCode):
			add("postal_code", i18n.KeyPostalCodeMalformed)
		}
		switch {
		case strings.TrimSpace(a.City) == "":
			add("city", i18n.KeyCityRequired)
		case utf8.RuneCountInString(a.City) > maxCityRunes:
			add("city", i18n.KeyAddressIncomplete)
		}
		switch {
		case strings.TrimSpace(a.District) == "":
			add("district", i18n.KeyDistrictRequired)
		case utf8.RuneCountInString(a.District) > maxDistrictRunes:
			add("district", i18n.KeyAddressIncomplete)
		}
		switch {
		case strings.TrimSpace(a.Street) == "":
			add("street", i18n.KeyStreetRequired)
		case utf8.RuneCountInString(a.Street) > maxStreetRunes:
			add("street", i18n.KeyStreetTooLong)
		}
	case destination.PickupPoint:
		errs = append(errs, a.pickupPointErrors()...)
	default:
		add("shipping", i18n.KeyChooseShipping)
	}
	return errs
}

// pickupPointErrors asks a shopper only for the chain. A store number and name
// come from the back office and are checked only when present, since the
// carrier's picker supplies them; once either is written,
// order_private_data_pickup_complete refuses a row without the other.
func (a *Delivery) pickupPointErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, web.FieldRefusal{Field: f, MessageKey: k}) }

	if !a.PickupChain.Known() {
		add("pickup_chain", i18n.KeyPickupChainRequired)
	}
	switch {
	case a.PickupStoreCode == "" && a.PickupStoreName != "":
		// "" satisfies neither half of the 1-to-10 digits-or-letters shape, so a
		// name with no code names the code field as what needs fixing.
		add("pickup_store_code", i18n.KeyStoreCodeMalformed)
	case a.PickupStoreCode != "" && a.PickupStoreName == "":
		add("pickup_store_name", i18n.KeyAddressIncomplete)
	case a.PickupStoreCode != "" && !pickup.ValidStoreCode(a.PickupStoreCode):
		add("pickup_store_code", i18n.KeyStoreCodeMalformed)
	}
	if utf8.RuneCountInString(a.PickupStoreName) > maxStoreNameRunes {
		add("pickup_store_name", i18n.KeyStoreNameTooLong)
	}
	return errs
}

// DropOtherDestination blanks the half of the address that does not apply, because
// order_private_data_one_destination refuses a row carrying both.
func (a *Delivery) DropOtherDestination() {
	switch a.To {
	case destination.Address:
		a.PickupChain, a.PickupStoreCode, a.PickupStoreName = "", "", ""
	case destination.PickupPoint:
		a.PostalCode, a.City, a.District, a.Street = "", "", "", ""
	}
}

// controlCharErrors: a newline in a name is how a shipping label gets a line it
// was never given.
func (a *Delivery) controlCharErrors() []web.FieldRefusal {
	var errs []web.FieldRefusal
	for _, f := range []struct{ name, value string }{
		{"email", a.Email}, {"name", a.RecipientName}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
		{"pickup_chain", string(a.PickupChain)}, {"pickup_store_code", a.PickupStoreCode},
		{"pickup_store_name", a.PickupStoreName}, {"note", a.Note},
	} {
		if hasControl(f.value) {
			errs = append(errs, web.FieldRefusal{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
		}
	}
	return errs
}

func emailError(s string) i18n.Key {
	switch {
	case strings.TrimSpace(s) == "":
		return i18n.KeyCheckoutEmailRequired
	case len(s) > maxInvoiceEmailBytes:
		return i18n.KeyCheckoutEmailTooLong
	case !email.Valid(s):
		return i18n.KeyCheckoutEmailMalformed
	}
	return ""
}

func looksLikePhone(s string) bool {
	if utf8.RuneCountInString(s) > maxPhoneRunes {
		return false
	}
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '-' || r == ' ' || r == '(' || r == ')' || r == '+':
		default:
			return false
		}
	}
	return digits >= 8 && digits <= 15
}

func isPostalCode(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > maxPostalCodeRunes {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Trim folds full-width digits in the phone and postal code and uppercases the
// store code, which pickup.ValidStoreCode will not fold.
func (a *Delivery) Trim() {
	a.Email = strings.TrimSpace(a.Email)
	a.RecipientName = strings.TrimSpace(a.RecipientName)
	a.Phone = strings.TrimSpace(web.FoldWidth(a.Phone))
	a.PostalCode = strings.TrimSpace(web.FoldWidth(a.PostalCode))
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
	a.PickupChain = pickup.Chain(strings.TrimSpace(string(a.PickupChain)))
	a.PickupStoreCode = strings.ToUpper(strings.TrimSpace(a.PickupStoreCode))
	a.PickupStoreName = strings.TrimSpace(a.PickupStoreName)
	a.Note = strings.TrimSpace(a.Note)
}

// hasControl reports whether s carries a control character. unicode.IsControl
// covers C1 (0x80–0x9F) as well as C0, which an ASCII-only check lets through.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
