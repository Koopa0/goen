package order

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/destination"
)

// TestDeliveryValidateRejects covers what the server must catch even though the
// browser was asked to catch it first. Each case is one field, so a failure
// names the rule that broke.
func TestDeliveryValidateRejects(t *testing.T) {
	t.Parallel()

	valid := Delivery{
		To:    destination.Address,
		Email: "a@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	for _, tt := range []struct {
		name  string
		mut   func(*Delivery)
		field string
	}{
		{"no email", func(a *Delivery) { a.Email = "" }, "email"},
		{"email with no @", func(a *Delivery) { a.Email = "nope" }, "email"},
		{"email with no domain dot", func(a *Delivery) { a.Email = "a@example" }, "email"},
		{"email with a space", func(a *Delivery) { a.Email = "a b@example.com" }, "email"},
		{"no name", func(a *Delivery) { a.RecipientName = "  " }, "name"},
		{"name too long", func(a *Delivery) { a.RecipientName = strings.Repeat("名", maxNameRunes+1) }, "name"},
		{"no phone", func(a *Delivery) { a.Phone = "" }, "phone"},
		{"phone with letters", func(a *Delivery) { a.Phone = "09abc12345" }, "phone"},
		{"phone too short", func(a *Delivery) { a.Phone = "12345" }, "phone"},
		{"phone too many digits", func(a *Delivery) { a.Phone = "1234567890123456" }, "phone"},
		{"phone representation too long", func(a *Delivery) {
			a.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes)
		}, "phone"},
		{"postal code not digits", func(a *Delivery) { a.PostalCode = "11A" }, "postal_code"},
		{"postal code too short", func(a *Delivery) { a.PostalCode = "11" }, "postal_code"},
		{"postal code too long", func(a *Delivery) { a.PostalCode = "1234567" }, "postal_code"},
		{"no city", func(a *Delivery) { a.City = "" }, "city"},
		{"city too long", func(a *Delivery) { a.City = strings.Repeat("市", maxCityRunes+1) }, "city"},
		{"no district", func(a *Delivery) { a.District = "" }, "district"},
		{"district too long", func(a *Delivery) { a.District = strings.Repeat("區", maxDistrictRunes+1) }, "district"},
		{"no street", func(a *Delivery) { a.Street = "" }, "street"},
		{"street too long", func(a *Delivery) { a.Street = strings.Repeat("路", maxStreetRunes+1) }, "street"},
		{"newline in the name", func(a *Delivery) { a.RecipientName = "王小明\nX" }, "name"},
		{"C1 control in the street", func(a *Delivery) { a.Street = "松高路\u0085 1 號" }, "street"},
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

// TestTheCheckoutRefusesWhatTheSenderWillRefuse holds one definition of an
// email address across the collection point and the delivery point.
//
// internal/email is that definition — it is what SMTPSender.Send tests before
// it will send anything. A checkout that accepts more than the sender does
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
