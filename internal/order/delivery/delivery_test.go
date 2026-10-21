package delivery

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web/formtext"
)

// TestDeliveryValidateRejects covers what the server must catch even though the
// browser was asked to catch it first. Each case is one field, so a failure
// names the rule that broke.
func TestDeliveryValidateRejects(t *testing.T) {
	t.Parallel()

	valid := Details{
		To:    shipping.DestinationAddress,
		Email: "a@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	for _, tt := range []struct {
		name  string
		mut   func(*Details)
		field string
	}{
		{"no email", func(a *Details) { a.Email = "" }, "email"},
		{"email with no @", func(a *Details) { a.Email = "nope" }, "email"},
		{"email with no domain dot", func(a *Details) { a.Email = "a@example" }, "email"},
		{"email with a space", func(a *Details) { a.Email = "a b@example.com" }, "email"},
		{"no name", func(a *Details) { a.RecipientName = "  " }, "name"},
		{"name too long", func(a *Details) { a.RecipientName = strings.Repeat("名", maxNameRunes+1) }, "name"},
		{"no phone", func(a *Details) { a.Phone = "" }, "phone"},
		{"phone with letters", func(a *Details) { a.Phone = "09abc12345" }, "phone"},
		{"phone too short", func(a *Details) { a.Phone = "12345" }, "phone"},
		{"phone too many digits", func(a *Details) { a.Phone = "1234567890123456" }, "phone"},
		{"phone representation too long", func(a *Details) {
			a.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes)
		}, "phone"},
		{"postal code not digits", func(a *Details) { a.PostalCode = "11A" }, "postal_code"},
		{"postal code too short", func(a *Details) { a.PostalCode = "11" }, "postal_code"},
		{"postal code too long", func(a *Details) { a.PostalCode = "1234567" }, "postal_code"},
		{"no city", func(a *Details) { a.City = "" }, "city"},
		{"city too long", func(a *Details) { a.City = strings.Repeat("市", maxCityRunes+1) }, "city"},
		{"no district", func(a *Details) { a.District = "" }, "district"},
		{"district too long", func(a *Details) { a.District = strings.Repeat("區", maxDistrictRunes+1) }, "district"},
		{"no street", func(a *Details) { a.Street = "" }, "street"},
		{"street too long", func(a *Details) { a.Street = strings.Repeat("路", maxStreetRunes+1) }, "street"},
		{"newline in the name", func(a *Details) { a.RecipientName = "王小明\nX" }, "name"},
		{"C1 control in the street", func(a *Details) { a.Street = "松高路\u0085 1 號" }, "street"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := valid
			tt.mut(&a)
			errs := a.Validate()
			if len(errs) == 0 {
				t.Fatalf("%s was accepted", tt.name)
			}
			var found bool
			for _, e := range errs {
				if e.Field == tt.field {
					found = true
				}
			}
			if !found {
				t.Errorf("rejected, but not on %q: %+v", tt.field, errs)
			}
		})
	}
}

// TestValidateRecipient is the one rule for who a parcel goes to and where,
// which the checkout and the account's address book both apply. Each input goes
// through Trim first, as both handlers do, and the want is the message the form
// shows beside the field ("" for accepted). Validate must say the same, since a
// saved address is offered at checkout as it was saved.
func TestValidateRecipient(t *testing.T) {
	t.Parallel()

	valid := Details{
		To: shipping.DestinationAddress, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	postal := func(s string) func(*Details) { return func(d *Details) { d.PostalCode = s } }

	for _, tt := range []struct {
		name  string
		mut   func(*Details)
		field string
		want  i18n.Key
	}{
		// Chunghwa Post's codes: three digits, the older 3+2 and the current 3+3.
		{"three-digit postal code", postal("100"), "postal_code", ""},
		{"3+2 postal code", postal("10617"), "postal_code", ""},
		{"3+3 postal code", postal("106409"), "postal_code", ""},
		// The shape is all this rule knows; a prefix no district uses is the
		// shipping zones' question (#2160).
		{"postal code no district uses", postal("000"), "postal_code", ""},
		{"full-width postal code", postal("１１０"), "postal_code", ""},
		{"full-width 3+3 postal code", postal("１０６４０９"), "postal_code", ""},
		{"mixed-width postal code", postal("１1０"), "postal_code", ""},
		{"postal code in spaces", postal(" 110 "), "postal_code", ""},
		{"postal code in a tab and a newline", postal("\t110\n"), "postal_code", ""},
		{"postal code in ideographic spaces", postal("　110　"), "postal_code", ""},
		{"blank postal code", postal(""), "postal_code", i18n.KeyPostalCodeRequired},
		{"spaces for a postal code", postal("   "), "postal_code", i18n.KeyPostalCodeRequired},
		{"an ideographic space for a postal code", postal("　"), "postal_code", i18n.KeyPostalCodeRequired},
		{"one digit", postal("1"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"two digits", postal("11"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"seven digits", postal("1234567"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"seven full-width digits", postal("１２３４５６７"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a letter", postal("11A"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a full-width letter", postal("１１Ａ"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a space inside", postal("11 0"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a hyphen inside", postal("106-409"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a plus sign", postal("+110"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"Arabic-Indic digits", postal("١١٠"), "postal_code", i18n.KeyPostalCodeMalformed},
		{"a NUL", postal("110\x00"), "postal_code", i18n.KeyPostalCodeMalformed},

		{"name at its limit", func(d *Details) { d.RecipientName = strings.Repeat("名", maxNameRunes) }, "name", ""},
		{"name past its limit", func(d *Details) { d.RecipientName = strings.Repeat("名", maxNameRunes+1) }, "name", i18n.KeyNameTooLong},
		{"phone at its limit", func(d *Details) { d.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes-10) }, "phone", ""},
		{"full-width phone", func(d *Details) { d.Phone = "０９１２３４５６７８" }, "phone", ""},
		{"city at its limit", func(d *Details) { d.City = strings.Repeat("市", maxCityRunes) }, "city", ""},
		{"district at its limit", func(d *Details) { d.District = strings.Repeat("區", maxDistrictRunes) }, "district", ""},
		{"street at its limit", func(d *Details) { d.Street = strings.Repeat("路", maxStreetRunes) }, "street", ""},
		{"control in the city", func(d *Details) { d.City = "台北\u0085市" }, "city", i18n.KeyFieldHasControlChars},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := valid
			tt.mut(&d)
			d.Trim()
			got := firstOn(d.ValidateRecipient(), tt.field)
			if got != tt.want {
				t.Errorf("ValidateRecipient says %q on %s, want %q", got, tt.field, tt.want)
			}
			d.Email = "a@example.com"
			if atCheckout := firstOn(d.Validate(), tt.field); atCheckout != got {
				t.Errorf("Validate says %q on %s, ValidateRecipient %q", atCheckout, tt.field, got)
			}
		})
	}

	// The order's own fields are Validate's alone: an address book has neither.
	d := valid
	d.Note = strings.Repeat("註", maxNoteRunes+1)
	if errs := d.ValidateRecipient(); len(errs) != 0 {
		t.Errorf("ValidateRecipient refused an address for its missing email or long note: %+v", errs)
	}
}

// firstOn is the message a form shows beside field: the first refusal naming it.
func firstOn(errs []formtext.FieldRefusal, field string) i18n.Key {
	for _, e := range errs {
		if e.Field == field {
			return e.MessageKey
		}
	}
	return ""
}

// TestTheCheckoutRefusesWhatTheSenderWillRefuse holds one definition of an
// email address across the collection point and the delivery point.
//
// internal/email is that definition — it is what letter.SMTPSender.Send tests
// before it will send anything. A checkout that accepts more than the sender does
// takes the order, writes the confirmation into the outbox in the order's own
// transaction, and then fails to deliver it on every one of StuckAfterAttempts before
// parking it on /admin/health. The customer is charged and never hears from the
// shop, and the confirmation is what carries Consumer Protection Act §18 I's
// disclosure.
func TestTheCheckoutRefusesWhatTheSenderWillRefuse(t *testing.T) {
	t.Parallel()

	// Each is accepted by a hand-rolled "has an @ and a dot" check and refused
	// by net/mail, because none of these is an atext character.
	for _, addr := range []string{
		"a,b@example.com",
		"a(b@example.com",
		"a;b@example.com",
		"a<b@example.com",
	} {
		if emailError(addr) == "" {
			t.Errorf("the checkout accepted %q, which internal/email refuses — the "+
				"order commits and its confirmation can never be delivered", addr)
		}
	}
	if got := emailError("shopper@example.com"); got != "" {
		t.Errorf("an ordinary address was refused: %v", got)
	}
}
