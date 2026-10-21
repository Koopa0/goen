// Package delivery is the form that says where and to whom an order goes: the
// recipient, their contact details, a street address or pickup point, and the
// note, with the checks the server applies to what the buyer typed.
package delivery

import (
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
)

// Details is where and to whom an order goes: order_private_data's recipient,
// street address or pickup point, and note.
type Details struct {
	// To picks which half is real; it comes from the shipping method, never the
	// form.
	To shipping.Destination

	Email         string
	RecipientName string
	Phone         string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     shipping.Chain
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

func (a *Details) Validate() []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	if k := emailError(a.Email); k != "" {
		errs = append(errs, formtext.FieldRefusal{Field: "email", MessageKey: k})
	}

	errs = append(errs, a.recipientErrors()...)

	if utf8.RuneCountInString(a.Note) > maxNoteRunes {
		errs = append(errs, formtext.FieldRefusal{Field: "note", MessageKey: i18n.KeyNoteTooLong})
	}

	fields := append([]field{{"email", a.Email}}, a.recipientFields()...)
	fields = append(fields, field{"note", a.Note})
	return append(errs, controlCharErrors(fields)...)
}

// ValidateRecipient checks who the parcel goes to and where: everything Validate
// checks but the order's own email and note. The account's address book saves
// only what passes it, so a saved address cannot fail at checkout.
func (a *Details) ValidateRecipient() []formtext.FieldRefusal {
	return append(a.recipientErrors(), controlCharErrors(a.recipientFields())...)
}

func (a *Details) recipientErrors() []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, formtext.FieldRefusal{Field: f, MessageKey: k}) }

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

	return append(errs, a.destinationErrors()...)
}

func (a *Details) destinationErrors() []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, formtext.FieldRefusal{Field: f, MessageKey: k}) }

	switch a.To {
	case shipping.DestinationAddress:
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
	case shipping.DestinationPickupPoint:
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
func (a *Details) pickupPointErrors() []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	add := func(f string, k i18n.Key) { errs = append(errs, formtext.FieldRefusal{Field: f, MessageKey: k}) }

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
	case a.PickupStoreCode != "" && !shipping.ValidStoreCode(a.PickupStoreCode):
		add("pickup_store_code", i18n.KeyStoreCodeMalformed)
	}
	if utf8.RuneCountInString(a.PickupStoreName) > maxStoreNameRunes {
		add("pickup_store_name", i18n.KeyStoreNameTooLong)
	}
	return errs
}

// DropOtherDestination blanks the half of the address that does not apply, because
// order_private_data_one_destination refuses a row carrying both.
func (a *Details) DropOtherDestination() {
	switch a.To {
	case shipping.DestinationAddress:
		a.PickupChain, a.PickupStoreCode, a.PickupStoreName = "", "", ""
	case shipping.DestinationPickupPoint:
		a.PostalCode, a.City, a.District, a.Street = "", "", "", ""
	}
}

type field struct{ name, value string }

// recipientFields are the fields ValidateRecipient checks, in form order.
func (a *Details) recipientFields() []field {
	return []field{
		{"name", a.RecipientName}, {"phone", a.Phone},
		{"postal_code", a.PostalCode}, {"city", a.City},
		{"district", a.District}, {"street", a.Street},
		{"pickup_chain", string(a.PickupChain)}, {"pickup_store_code", a.PickupStoreCode},
		{"pickup_store_name", a.PickupStoreName},
	}
}

// controlCharErrors: a newline in a name is how a shipping label gets a line it
// was never given.
func controlCharErrors(fields []field) []formtext.FieldRefusal {
	var errs []formtext.FieldRefusal
	for _, f := range fields {
		if formtext.HasControlChars(f.value) {
			errs = append(errs, formtext.FieldRefusal{Field: f.name, MessageKey: i18n.KeyFieldHasControlChars})
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
// store code, which shipping.ValidStoreCode will not fold.
func (a *Details) Trim() {
	a.Email = strings.TrimSpace(a.Email)
	a.RecipientName = strings.TrimSpace(a.RecipientName)
	a.Phone = strings.TrimSpace(formtext.FoldWidth(a.Phone))
	a.PostalCode = strings.TrimSpace(formtext.FoldWidth(a.PostalCode))
	a.City = strings.TrimSpace(a.City)
	a.District = strings.TrimSpace(a.District)
	a.Street = strings.TrimSpace(a.Street)
	a.PickupChain = shipping.Chain(strings.TrimSpace(string(a.PickupChain)))
	a.PickupStoreCode = strings.ToUpper(strings.TrimSpace(a.PickupStoreCode))
	a.PickupStoreName = strings.TrimSpace(a.PickupStoreName)
	a.Note = strings.TrimSpace(a.Note)
}
