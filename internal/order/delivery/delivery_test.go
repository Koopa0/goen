package delivery

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/web/formtext"
)

// TestDetails_ValidateRejects covers what the server must catch even though the
// browser was asked to catch it first. Each case is one field, so a failure
// names the rule that broke.
func TestDetails_ValidateRejects(t *testing.T) {
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

// TestDetails_ValidateRecipient is the one rule for who a parcel goes to and where,
// which the checkout and the account's address book both apply. Each input goes
// through Trim first, as both handlers do, and the want is the message the form
// shows beside the field ("" for accepted). Validate must say the same, since a
// saved address is offered at checkout as it was saved.
func TestDetails_ValidateRecipient(t *testing.T) {
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

// TestEmailErrorSenderRefusals holds that the checkout refuses what the sender
// will refuse. It holds one definition of an email address across the
// collection point and the delivery point.
//
// internal/email is that definition — it is what letter.SMTPSender.Send tests
// before it will send anything. A checkout that accepts more than the sender does
// takes the order, writes the confirmation into the outbox in the order's own
// transaction, and then fails to deliver it on every one of StuckAfterAttempts before
// parking it on /admin/health. The customer is charged and never hears from the
// shop, and the confirmation is what carries Consumer Protection Act §18 I's
// disclosure.
func TestEmailErrorSenderRefusals(t *testing.T) {
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

// TestDetails_Line holds that a pickup order is named by its chain. It holds
// what the confirmation and the back office show while no store picker exists:
// the chain is the destination, and a store appears only when one is known.
func TestDetails_Line(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		d    Details
		want string
	}{
		{
			name: "the chain alone",
			d:    Details{PickupChain: shipping.ChainSevenEleven},
			want: "7-ELEVEN",
		},
		{
			name: "a chain whose store is known",
			d: Details{
				PickupChain: shipping.ChainFamilyMart, PickupStoreCode: "012345",
				PickupStoreName: "台北車站門市",
			},
			want: "全家 FamilyMart 台北車站門市(012345)",
		},
		{
			name: "an address",
			d: Details{
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			},
			want: "110 台北市信義區松高路 1 號",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.d.Line(); got != tt.want {
				t.Errorf("Line() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDetails_ValidateBlankPostcode: Details.Validate asks for a blank postcode
// as a missing answer rather than a malformed one, and calls a short postcode
// malformed.
func TestDetails_ValidateBlankPostcode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		postal string
		want   i18n.Key
	}{
		{"", i18n.KeyPostalCodeRequired},
		{"   ", i18n.KeyPostalCodeRequired},
		{"11", i18n.KeyPostalCodeMalformed},
	} {
		a := Details{
			To:    shipping.DestinationAddress,
			Email: "a@example.com", RecipientName: "王小明", Phone: "0912345678",
			PostalCode: tt.postal, City: "台北市", District: "信義區", Street: "松高路 1 號",
		}
		var got i18n.Key
		for _, e := range a.Validate() {
			if e.Field == "postal_code" {
				got = e.MessageKey
			}
		}
		if got != tt.want {
			t.Errorf("postcode %q says %s, want %s", tt.postal, got, tt.want)
		}
	}
}

// TestDetails_ValidateAccepts: Details.Validate accepts a valid address, a long
// email with a tag and a convenience-store pickup that has no street. It is the
// control: a Validate that rejected everything would pass every case above.
func TestDetails_ValidateAccepts(t *testing.T) {
	t.Parallel()

	for _, a := range []Details{
		{To: shipping.DestinationAddress, Email: "a@example.com", RecipientName: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"},
		{To: shipping.DestinationAddress, Email: "someone.long+tag@sub.example.co.uk", RecipientName: "Li Hua", Phone: "+886 2 2700-1234",
			PostalCode: "10041", City: "台北市", District: "中正區", Street: "重慶南路一段 122 號",
			Note: "請放管理室"},
		// Convenience-store pickup, which has no street at all.
		{To: shipping.DestinationPickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
			PickupChain: "family_mart", PickupStoreCode: "012345", PickupStoreName: "台北車站門市"},
	} {
		if errs := a.Validate(); len(errs) != 0 {
			t.Errorf("a valid address was rejected: %+v", errs)
		}
	}
}

// TestDetails_ValidatePickupChain: Details.Validate accepts every known pickup
// chain and refuses an unknown one with a pickup_chain error.
func TestDetails_ValidatePickupChain(t *testing.T) {
	chains := shipping.Chains()
	if len(chains) == 0 {
		t.Fatal("shipping.Chains() is empty; the test needs a known chain")
	}
	for _, chain := range chains {
		address := Details{
			To: shipping.DestinationPickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
			PickupChain: chain, PickupStoreCode: "012345", PickupStoreName: "台北車站門市",
		}
		if errs := address.Validate(); len(errs) != 0 {
			t.Errorf("known chain %q is invalid: %+v", chain, errs)
		}
	}

	address := Details{
		To: shipping.DestinationPickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
		PickupChain: "other_chain", PickupStoreCode: "012345", PickupStoreName: "台北車站門市",
	}
	errs := address.Validate()
	var refused bool
	for _, err := range errs {
		if err.Field == "pickup_chain" {
			refused = true
			break
		}
	}
	if !refused {
		t.Errorf("an unknown pickup chain was valid: %+v", errs)
	}
}

// TestDetails_ValidateDestination: Details.Validate asks for the fields of the
// destination the method needs, with its own fields and with the other
// destination's, and reports no others. It covers each destination with its own
// fields and with the OTHER destination's, which is what a customer who filled
// one section and then switched methods submits.
func TestDetails_ValidateDestination(t *testing.T) {
	contact := func(a *Details) {
		a.Email, a.RecipientName, a.Phone = "who@example.com", "王小明", "0912345678"
	}
	address := func(a *Details) {
		a.PostalCode, a.City, a.District, a.Street = "110", "台北市", "信義區", "松高路 1 號"
	}
	pickupAddress := func(a *Details) {
		a.PickupChain, a.PickupStoreCode, a.PickupStoreName = "seven_eleven", "123456", "信義門市"
	}
	chainOnly := func(a *Details) { a.PickupChain = "seven_eleven" }

	cases := []struct {
		name  string
		to    shipping.Destination
		fill  []func(*Details)
		wants []string // the fields that must be reported, and no others
	}{
		{
			name: "an address order with an address",
			to:   shipping.DestinationAddress,
			fill: []func(*Details){contact, address},
		},
		{
			name: "a pickup order with a store",
			to:   shipping.DestinationPickupPoint,
			fill: []func(*Details){contact, pickupAddress},
		},
		{
			name:  "an address order carrying only a store",
			to:    shipping.DestinationAddress,
			fill:  []func(*Details){contact, pickupAddress},
			wants: []string{"postal_code", "city", "district", "street"},
		},
		{
			name: "a pickup order carrying the chain alone",
			to:   shipping.DestinationPickupPoint,
			fill: []func(*Details){contact, chainOnly},
		},
		{
			name:  "a pickup order carrying only an address",
			to:    shipping.DestinationPickupPoint,
			fill:  []func(*Details){contact, address},
			wants: []string{"pickup_chain"},
		},
		{
			name:  "a method whose destination is unknown here",
			to:    shipping.Destination("depot"),
			fill:  []func(*Details){contact, address, pickupAddress},
			wants: []string{"shipping"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Details{To: c.to}
			for _, f := range c.fill {
				f(a)
			}
			got := make([]string, 0, 4)
			for _, e := range a.Validate() {
				got = append(got, e.Field)
			}
			slices.Sort(got)
			want := slices.Clone(c.wants)
			slices.Sort(want)
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("rejected fields (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDetails_ValidateStoreCode: Details.Validate accepts a store code in
// whatever form the chain numbers its stores, Hi-Life's lettered codes among
// them and in either case, and refuses a store name typed into the code field
// or a code that is too long. The store codes below are REAL, read from ECPay's
// own GetStoreList on 2026-08-06: Hi-Life numbers its stores in four characters
// and 149 of its 1,350 lead with a letter, so S884 must stay red under a
// digits-only rule.
func TestDetails_ValidateStoreCode(t *testing.T) {
	cases := []struct {
		name   string
		chain  shipping.Chain
		code   string
		refuse bool
	}{
		{name: "7-ELEVEN 千禧", chain: "seven_eleven", code: "110080"},
		{name: "全家 永和保安店", chain: "family_mart", code: "010855"},
		{name: "OK 福林店", chain: "ok_mart", code: "000002"},
		{name: "萊爾富 高縣後庄店", chain: "hi_life", code: "S884"},
		{name: "萊爾富, another lettered code", chain: "hi_life", code: "H869"},
		// Trim uppercases, so a shift key is not a rejected checkout.
		{name: "萊爾富 高縣後庄店 typed in lower case", chain: "hi_life", code: "s884"},

		{name: "a 店名 typed into the code field", chain: "seven_eleven", code: "信義門市", refuse: true},
		// The chain is the whole choice a shopper makes; a code arrives from the
		// back office, or from the carrier's picker when that is integrated.
		{name: "no code at all", chain: "seven_eleven", code: ""},
		{name: "longer than a 門市代碼", chain: "seven_eleven", code: "11008011008", refuse: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A name pairs with the code under test, except for "no code at
			// all": with no code, a name would itself be the half-written
			// pickup point order_private_data_pickup_complete refuses, which
			// is a different case entirely from what a code's own shape locks.
			storeName := "門市"
			if c.code == "" {
				storeName = ""
			}
			a := &Details{
				To:              shipping.DestinationPickupPoint,
				Email:           "who@example.com",
				RecipientName:   "王小明",
				Phone:           "0912345678",
				PickupChain:     c.chain,
				PickupStoreCode: c.code,
				PickupStoreName: storeName,
			}
			a.Trim()

			var want []string
			if c.refuse {
				want = []string{"pickup_store_code"}
			}
			got := make([]string, 0, 1)
			for _, e := range a.Validate() {
				got = append(got, e.Field)
			}
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Validate() rejected fields for code %q (-want +got):\n%s", c.code, diff)
			}
		})
	}
}

// TestDetails_ValidateStorePair: Details.Validate refuses a pickup store code
// with no name and a name with no code, and accepts both or neither. It locks
// the tightened shape of order_private_data_pickup_complete at the form: once
// the picker writes one of the store code or store name, checkout must refuse a
// submission that does not also carry the other, rather than let the database
// be the first thing to say so.
func TestDetails_ValidateStorePair(t *testing.T) {
	t.Parallel()

	base := Details{
		To: shipping.DestinationPickupPoint, Email: "who@example.com", RecipientName: "王小明", Phone: "0912345678",
		PickupChain: "seven_eleven",
	}

	for _, tt := range []struct {
		name  string
		code  string
		store string
		field string // empty means the submission is accepted
	}{
		{name: "chain alone, neither written yet", code: "", store: ""},
		{name: "code and name both written", code: "123456", store: "信義門市"},
		{name: "a code with no name", code: "123456", store: "", field: "pickup_store_name"},
		{name: "a name with no code", code: "", store: "信義門市", field: "pickup_store_code"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := base
			a.PickupStoreCode, a.PickupStoreName = tt.code, tt.store

			errs := a.Validate()
			if tt.field == "" {
				if len(errs) != 0 {
					t.Fatalf("a legal pickup point was rejected: %+v", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != tt.field {
				t.Fatalf("Validate() = %+v, want exactly one error on %q", errs, tt.field)
			}
		})
	}
}

// TestDetails_DropOtherDestination: DropOtherDestination clears the address of
// a pickup order and the pickup point of an address order, and keeps the half
// that belongs to the destination. Validation passes on a submission carrying
// both, so this is what stops the pair reaching
// order_private_data_one_destination.
func TestDetails_DropOtherDestination(t *testing.T) {
	both := func(to shipping.Destination) *Details {
		return &Details{
			To: to, Email: "e@example.com", RecipientName: "n", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			PickupChain: "seven_eleven", PickupStoreCode: "123456", PickupStoreName: "信義門市",
		}
	}

	a := both(shipping.DestinationPickupPoint)
	a.DropOtherDestination()
	if a.Street != "" || a.City != "" || a.District != "" || a.PostalCode != "" {
		t.Errorf("a pickup order kept an address: %q %q %q %q",
			a.PostalCode, a.City, a.District, a.Street)
	}
	if a.PickupStoreCode != "123456" {
		t.Errorf("the pickup point was dropped: %q", a.PickupStoreCode)
	}

	b := both(shipping.DestinationAddress)
	b.DropOtherDestination()
	if b.PickupChain != "" || b.PickupStoreCode != "" || b.PickupStoreName != "" {
		t.Errorf("an address order kept a pickup point: %q %q %q",
			b.PickupChain, b.PickupStoreCode, b.PickupStoreName)
	}
	if b.Street != "松高路 1 號" {
		t.Errorf("the address was dropped: %q", b.Street)
	}
}

// TestDetails_ValidateBlankCity: Details.Validate refuses a blank city with the
// wording of a free-text field: asked to be filled in, not chosen. It holds the
// wording of a free-text field: the city and district are typed, so neither is
// asked to be chosen.
func TestDetails_ValidateBlankCity(t *testing.T) {
	t.Parallel()

	addr := Details{To: shipping.DestinationAddress, PostalCode: "110", District: "信義區", Street: "松高路 1 號"}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var got string
	for _, e := range addr.Validate() {
		if e.Field == "city" {
			got = i18n.T(ctx, e.MessageKey)
		}
	}
	if got != "請填寫縣市。" {
		t.Errorf("a blank city is refused with %q, want 請填寫縣市。", got)
	}
}
