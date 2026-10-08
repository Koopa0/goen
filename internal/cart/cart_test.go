package cart

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"

	accountpkg "github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestCheckoutQuoteIdentityNamesEveryCommercialFact(t *testing.T) {
	t.Parallel()
	base := CheckoutQuote{
		CartID: uuid.MustParse("018f0000-0000-7000-8000-000000000001"),
		Lines: []CheckoutQuoteLine{
			{VariantID: uuid.MustParse("018f0000-0000-7000-8000-000000000011"), Quantity: 2, UnitCents: 12000},
			{VariantID: uuid.MustParse("018f0000-0000-7000-8000-000000000012"), Quantity: 1, UnitCents: 30000},
		},
		ShippingVersionID: uuid.MustParse("018f0000-0000-7000-8000-000000000021"),
		ShippingCents:     6000,
		CouponCode:        "SAVE10",
		DiscountCents:     5000,
		CreditCents:       7000,
	}
	want, err := base.ID()
	if err != nil {
		t.Fatalf("base quote ID: %v", err)
	}

	clone := func() CheckoutQuote {
		q := base
		q.Lines = slices.Clone(base.Lines)
		return q
	}
	tests := []struct {
		name   string
		change func(*CheckoutQuote)
	}{
		{"cart", func(q *CheckoutQuote) { q.CartID = uuid.New() }},
		{"variant", func(q *CheckoutQuote) { q.Lines[0].VariantID = uuid.New() }},
		{"quantity", func(q *CheckoutQuote) { q.Lines[0].Quantity++ }},
		{"unit price", func(q *CheckoutQuote) { q.Lines[0].UnitCents++ }},
		{"shipping version", func(q *CheckoutQuote) { q.ShippingVersionID = uuid.New() }},
		{"shipping amount", func(q *CheckoutQuote) { q.ShippingCents++ }},
		{"coupon code", func(q *CheckoutQuote) { q.CouponCode = "OTHER10" }},
		{"discount", func(q *CheckoutQuote) { q.DiscountCents++ }},
		{"credit", func(q *CheckoutQuote) { q.CreditCents++ }},
		{"same gross, different components", func(q *CheckoutQuote) {
			q.ShippingCents += 100
			q.DiscountCents += 100
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := clone()
			tt.change(&q)
			got, idErr := q.ID()
			if idErr != nil {
				t.Fatalf("changed quote ID: %v", idErr)
			}
			if got == want {
				t.Error("changed commercial fact retained the old quote ID")
			}
		})
	}
}

func TestCheckoutQuoteIdentityIsCanonicalAndDoesNotAliasLines(t *testing.T) {
	t.Parallel()
	first := uuid.MustParse("018f0000-0000-7000-8000-000000000011")
	second := uuid.MustParse("018f0000-0000-7000-8000-000000000012")
	quote := CheckoutQuote{
		CartID: uuid.New(),
		Lines: []CheckoutQuoteLine{
			{VariantID: second, Quantity: 1, UnitCents: 200},
			{VariantID: first, Quantity: 2, UnitCents: 100},
		},
		ShippingVersionID: uuid.New(),
		ShippingCents:     60,
	}
	original := slices.Clone(quote.Lines)
	id, err := quote.ID()
	if err != nil {
		t.Fatalf("quote ID: %v", err)
	}
	if diff := cmp.Diff(original, quote.Lines); diff != "" {
		t.Fatalf("ID mutated caller lines (-want +got):\n%s", diff)
	}
	reversed := quote
	reversed.Lines = slices.Clone(quote.Lines)
	slices.Reverse(reversed.Lines)
	reversedID, err := reversed.ID()
	if err != nil {
		t.Fatalf("reversed quote ID: %v", err)
	}
	if reversedID != id {
		t.Error("line presentation order changed a canonical quote ID")
	}
}

func TestCheckoutQuoteRejectsAmbiguousShapes(t *testing.T) {
	t.Parallel()
	line := CheckoutQuoteLine{VariantID: uuid.New(), Quantity: 1, UnitCents: 100}
	base := CheckoutQuote{
		CartID: uuid.New(), Lines: []CheckoutQuoteLine{line},
		ShippingVersionID: uuid.New(),
	}
	tests := []struct {
		name   string
		change func(*CheckoutQuote)
	}{
		{"empty", func(q *CheckoutQuote) { q.Lines = nil }},
		{"duplicate variant", func(q *CheckoutQuote) { q.Lines = append(q.Lines, line) }},
		{"zero quantity", func(q *CheckoutQuote) { q.Lines[0].Quantity = 0 }},
		{"negative unit price", func(q *CheckoutQuote) { q.Lines[0].UnitCents = -1 }},
		{"noncanonical coupon", func(q *CheckoutQuote) { q.CouponCode = " save10 " }},
		{"discount without coupon", func(q *CheckoutQuote) { q.DiscountCents = 1 }},
		{"credit above gross", func(q *CheckoutQuote) { q.CreditCents = 101 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := base
			q.Lines = slices.Clone(base.Lines)
			tt.change(&q)
			if _, err := q.ID(); err == nil {
				t.Error("invalid quote was accepted")
			}
		})
	}
}

func TestCheckoutQuoteIDFormEncodingIsFixedAndCanonical(t *testing.T) {
	t.Parallel()
	quote := CheckoutQuote{
		CartID:            uuid.New(),
		Lines:             []CheckoutQuoteLine{{VariantID: uuid.New(), Quantity: 1, UnitCents: 100}},
		ShippingVersionID: uuid.New(),
	}
	id, err := quote.ID()
	if err != nil {
		t.Fatalf("quote ID: %v", err)
	}
	encoded := id.String()
	if len(encoded) != 43 {
		t.Fatalf("encoded length = %d, want 43", len(encoded))
	}
	parsed, err := parseCheckoutQuoteID(encoded)
	if err != nil || parsed != id {
		t.Fatalf("round trip = %v, %v", parsed, err)
	}
	for _, malformed := range []string{"", "not-base64!", encoded + "=", (CheckoutQuoteID{}).String()} {
		if _, err := parseCheckoutQuoteID(malformed); err == nil {
			t.Errorf("parseCheckoutQuoteID(%q) accepted a malformed ID", malformed)
		}
	}
}

func TestCheckoutAttemptIDFormEncodingIsFixedAndCanonical(t *testing.T) {
	t.Parallel()
	id, err := newCheckoutAttemptID()
	if err != nil {
		t.Fatalf("new checkout attempt ID: %v", err)
	}
	encoded := id.String()
	if len(encoded) != 22 {
		t.Fatalf("encoded length = %d, want 22", len(encoded))
	}
	parsed, err := parseCheckoutAttemptID(encoded)
	if err != nil || parsed != id {
		t.Fatalf("round trip = %v, %v", parsed, err)
	}

	for _, malformed := range []string{
		"",
		"   ",
		strings.Repeat("A", 4096),
		encoded + "=",
		"not-base64!",
		(checkoutAttemptID{}).String(),
	} {
		if _, parseErr := parseCheckoutAttemptID(malformed); parseErr == nil {
			t.Errorf("parseCheckoutAttemptID(%q) accepted a malformed ID", malformed)
		}
	}
}

func TestCheckoutMoneyRejectsEveryOverflowBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		add  func() (int64, error)
	}{
		{"line multiplication", func() (int64, error) {
			return addCheckoutLine(0, math.MaxInt64, 2)
		}},
		// (2^62+1)×4 wraps to 4: a product that lands small and positive, which
		// no later sign check can tell from a real four-cent line.
		{"line multiplication wrapping to a small positive", func() (int64, error) {
			return addCheckoutLine(0, 1<<62+1, 4)
		}},
		{"subtotal accumulation", func() (int64, error) {
			return addCheckoutLine(math.MaxInt64, 1, 1)
		}},
		{"shipping total", func() (int64, error) {
			return (Quote{FeeCents: math.MaxInt64, Surcharge: 1}).Total()
		}},
		{"checkout gross", func() (int64, error) {
			return checkoutGross(math.MaxInt64, 1, 0)
		}},
		{"discount above gross", func() (int64, error) {
			return checkoutGross(1, 0, 2)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tt.add(); !errors.Is(err, errCheckoutMoney) {
				t.Fatalf("error = %v, want errCheckoutMoney", err)
			}
		})
	}
}

func TestHoldTTLIncludesTheWholePaymentSafetyBudget(t *testing.T) {
	t.Parallel()
	if payWindow != 29*time.Minute {
		t.Errorf("payWindow = %s, want 29m", payWindow)
	}
	if stripeSessionFloor != 30*time.Minute {
		t.Errorf("stripeSessionFloor = %s, want 30m", stripeSessionFloor)
	}
	if stripeSessionStartMargin != time.Minute {
		t.Errorf("stripeSessionStartMargin = %s, want 1m", stripeSessionStartMargin)
	}
	if holdTTL != payWindow+stripeSessionFloor+stripeSessionStartMargin {
		t.Errorf("holdTTL = %s, want pay window + Stripe floor + start margin", holdTTL)
	}
	stated, err := strconv.Atoi(pages.HoldMinutesText())
	if err != nil {
		t.Fatalf("parse customer hold duration: %v", err)
	}
	if got := int(holdTTL.Minutes()); got != stated {
		t.Errorf("holdTTL = %d minutes, customer policy says %d", got, stated)
	}
}

func TestHashTokenIsNotTheToken(t *testing.T) {
	t.Parallel()

	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	h := HashToken(tok)
	if len(h) != 32 {
		t.Errorf("HashToken length = %d, want 32", len(h))
	}
	if strings.Contains(string(h), tok) {
		t.Error("the digest contains the token; the database would hold live cart cookies")
	}
	if !bytes.Equal(HashToken(tok), h) {
		t.Error("HashToken is not deterministic; a returning visitor would lose their cart")
	}
	if bytes.Equal(HashToken(tok+"x"), h) {
		t.Error("two different tokens hash the same")
	}
}

func TestNewTokenIsUnpredictable(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for range 64 {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token %q is only %d characters; too little entropy to guard a cart", tok, len(tok))
		}
		if seen[tok] {
			t.Fatal("NewToken repeated a token")
		}
		seen[tok] = true
	}
}

func TestParseQuantity(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   string
		want int32
	}{
		{"1", 1},
		{"3", 3},
		{"999", 999},
		{"1000", MaxLineQuantity}, // clamped to the schema's own ceiling
		{"0", 1},                  // the button means "add one"
		{"-5", 1},
		{"", 1},
		{"abc", 1},
		{" 2 ", 2},
	} {
		if got := ParseQuantity(tt.in); got != tt.want {
			t.Errorf("ParseQuantity(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseQuantityAllowingZero(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in     string
		want   int32
		wantOK bool
	}{
		{"0", 0, true}, // on the cart page zero means remove
		{"2", 2, true},
		{"1000", MaxLineQuantity, true},
		{"-1", 0, false},
		{"x", 0, false},
	} {
		got, ok := ParseQuantityAllowingZero(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ParseQuantityAllowingZero(%q) = %d/%v, want %d/%v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestShippingFee(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name                          string
		fee, freeOver, subtotal, want int64
	}{
		{"below the threshold pays", 8000, 300000, 299900, 8000},
		{"exactly the threshold is free", 8000, 300000, 300000, 0},
		{"above the threshold is free", 8000, 300000, 500000, 0},
		{"no threshold always pays", 8000, 0, 900000, 8000},
		{"a free method is free", 0, 0, 100, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ShippingFee(tt.fee, tt.freeOver, tt.subtotal); got != tt.want {
				t.Errorf("ShippingFee(%d, %d, %d) = %d, want %d",
					tt.fee, tt.freeOver, tt.subtotal, got, tt.want)
			}
		})
	}
}

// A blank postcode is a missing answer, not a malformed one.
func TestABlankPostcodeIsAskedForRatherThanCorrected(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		postal string
		want   i18n.Key
	}{
		{"", i18n.KeyPostalCodeRequired},
		{"   ", i18n.KeyPostalCodeRequired},
		{"11", i18n.KeyPostalCodeMalformed},
	} {
		a := order.Delivery{
			To:    destination.Address,
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

// TestSavedHomeAddressContractMatchesCheckout is the cross-package guard for
// the address-book handoff. A signed-in shopper must never choose an address
// the account package accepted only to have checkout refuse the same fields.
func TestSavedHomeAddressContractMatchesCheckout(t *testing.T) {
	t.Parallel()

	// The checkout limits, as order.Delivery states them.
	const maxPhoneRunes, maxCityRunes, maxDistrictRunes, maxStreetRunes = 30, 20, 20, 200

	base := accountpkg.Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	tests := []struct {
		name string
		want bool
		mut  func(*accountpkg.Address)
	}{
		{name: "ordinary Taiwan address", want: true},
		{name: "current six-digit postal code", want: true, mut: func(a *accountpkg.Address) {
			a.PostalCode = "110204"
		}},
		{name: "internationally formatted Taiwan phone", want: true, mut: func(a *accountpkg.Address) {
			a.Phone = "+886 (2) 2700-1234"
		}},
		{name: "surrounding form whitespace", want: true, mut: func(a *accountpkg.Address) {
			a.Name, a.Phone, a.PostalCode = " 王小明 ", " 0912345678 ", " 110 "
			a.City, a.District, a.Street = " 台北市 ", " 信義區 ", " 松高路 1 號 "
		}},
		{name: "short phone", mut: func(a *accountpkg.Address) { a.Phone = "02-12345" }},
		{name: "phone letters", mut: func(a *accountpkg.Address) { a.Phone = "09AB123456" }},
		{name: "too many phone digits", mut: func(a *accountpkg.Address) {
			a.Phone = "1234567890123456"
		}},
		{name: "unbounded phone punctuation", mut: func(a *accountpkg.Address) {
			a.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes)
		}},
		{name: "short postal code", mut: func(a *accountpkg.Address) { a.PostalCode = "11" }},
		{name: "postal code letters", mut: func(a *accountpkg.Address) { a.PostalCode = "11A" }},
		{name: "long postal code", mut: func(a *accountpkg.Address) { a.PostalCode = "1234567" }},
		{name: "long city", mut: func(a *accountpkg.Address) {
			a.City = strings.Repeat("市", maxCityRunes+1)
		}},
		{name: "long district", mut: func(a *accountpkg.Address) {
			a.District = strings.Repeat("區", maxDistrictRunes+1)
		}},
		{name: "long street", mut: func(a *accountpkg.Address) {
			a.Street = strings.Repeat("路", maxStreetRunes+1)
		}},
		{name: "control in street", mut: func(a *accountpkg.Address) {
			a.Street = "松高路\u0085 1 號"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			saved := base
			if tt.mut != nil {
				tt.mut(&saved)
			}
			saved.Trim()
			checkout := order.Delivery{
				To: destination.Address, Email: "buyer@example.com",
				RecipientName: saved.Name, Phone: saved.Phone, PostalCode: saved.PostalCode,
				City: saved.City, District: saved.District, Street: saved.Street,
			}
			checkout.Trim()

			savedOK := len(saved.Validate()) == 0
			checkoutOK := len(checkout.Validate()) == 0
			if savedOK != tt.want {
				t.Errorf("account acceptance = %t, want %t", savedOK, tt.want)
			}
			if checkoutOK != tt.want {
				t.Errorf("checkout acceptance = %t, want %t", checkoutOK, tt.want)
			}
			if savedOK && !checkoutOK {
				t.Error("account accepted a HOME address checkout refused")
			}
		})
	}
}

// TestAddressValidateAccepts is the control: a Validate that rejected everything
// would pass every case above.
func TestAddressValidateAccepts(t *testing.T) {
	t.Parallel()

	for _, a := range []order.Delivery{
		{To: destination.Address, Email: "a@example.com", RecipientName: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"},
		{To: destination.Address, Email: "someone.long+tag@sub.example.co.uk", RecipientName: "Li Hua", Phone: "+886 2 2700-1234",
			PostalCode: "10041", City: "台北市", District: "中正區", Street: "重慶南路一段 122 號",
			Note: "請放管理室"},
		// Convenience-store pickup, which has no street at all.
		{To: destination.PickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
			PickupChain: "family_mart", PickupStoreCode: "012345", PickupStoreName: "台北車站門市"},
	} {
		if errs := a.Validate(); len(errs) != 0 {
			t.Errorf("a valid address was rejected: %+v", errs)
		}
	}
}

// TestCheckoutShowsOneMessagePerField locks the reduction a refused checkout
// renders: a control that broke two rules names the first of them, so the form
// shows one reason per field rather than a pile.
func TestCheckoutShowsOneMessagePerField(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The phone is malformed AND carries a control character, in that order.
	addr := &order.Delivery{
		To: destination.Address, Email: "a@example.com", RecipientName: "王小明",
		Phone: "09\x0712345678", PostalCode: "110", City: "台北市",
		District: "信義區", Street: "松高路 1 號",
	}
	want := map[string]string{
		"phone":    i18n.T(ctx, i18n.KeyPhoneMalformed),
		"shipping": i18n.T(ctx, i18n.KeyChooseShipping),
	}

	got := checkoutErrors(ctx, addr, errors.New("no shipping method chosen"), &Invoice{})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("checkoutErrors (-want +got):\n%s", diff)
	}
}

func TestEveryOfferedPickupChainPassesAddressValidation(t *testing.T) {
	chains := pickup.Offered()
	if len(chains) == 0 {
		t.Fatal("pickup.Offered() is empty; the test needs an offered chain")
	}
	for _, chain := range chains {
		address := order.Delivery{
			To: destination.PickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
			PickupChain: chain, PickupStoreCode: "012345", PickupStoreName: "台北車站門市",
		}
		if errs := address.Validate(); len(errs) != 0 {
			t.Errorf("offered chain %q is invalid: %+v", chain, errs)
		}
	}

	address := order.Delivery{
		To: destination.PickupPoint, Email: "pick@example.com", RecipientName: "陳小明", Phone: "0933444555",
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

func TestInvoiceChoicesMatchCheckoutValidation(t *testing.T) {
	choices := invoiceChoices(i18n.WithLocale(t.Context(), i18n.ZhHant))
	offered := invoice.OfferedPreferences()
	if len(choices) != len(offered) {
		t.Fatalf("invoiceChoices length = %d, want %d", len(choices), len(offered))
	}

	for i, preference := range offered {
		choice := choices[i]
		if choice.Value != preference || choice.Label == "" {
			t.Errorf("invoice choice %d = %+v, want value %q and a label", i, choice, preference)
		}
		candidate := Invoice{Type: preference}
		if preference.NeedsMobileBarcode() {
			candidate.MobileBarcode = "/AB12345"
		}
		if preference == invoice.PreferenceDonate {
			candidate.DonationCode = "00123"
		}
		if preference.NeedsTaxID() {
			candidate.CompanyName = "測試股份有限公司"
			candidate.TaxID = "04595252"
		}
		if errs := candidate.Validate(); len(errs) != 0 {
			t.Errorf("offered invoice preference %q is invalid: %+v", preference, errs)
		}
	}

	unknown := Invoice{Type: "paper"}
	if errs := unknown.Validate(); len(errs) != 1 || errs[0].Field != "invoice_type" {
		t.Errorf("unknown invoice preference errors = %+v, want invoice_type", errs)
	}

	company := Invoice{Type: invoice.PreferenceCompany, TaxID: "12345678"}
	errs := company.Validate()
	if len(errs) != 2 || errs[0].Field != "invoice_company_name" ||
		errs[1].Field != "invoice_tax_id" {
		t.Errorf("invalid company invoice errors = %+v, want company name then checksum", errs)
	}

	special := Invoice{
		Type: invoice.PreferenceCompany, CompanyName: " 第七碼公司 ", TaxID: "10458570",
	}
	if errs := special.Validate(); len(errs) != 0 || special.CompanyName != "第七碼公司" {
		t.Errorf("current-MOF company invoice = %+v / %+v, want valid and trimmed", special, errs)
	}

	controlled := Invoice{
		Type: invoice.PreferenceCompany, CompanyName: "買受\n公司", TaxID: "04595252",
	}
	if errs := controlled.Validate(); len(errs) != 1 || errs[0].Field != "invoice_company_name" {
		t.Errorf("controlled company name errors = %+v, want invoice_company_name", errs)
	}

	member := Invoice{
		Type: invoice.PreferenceMember, MobileBarcode: "/AB12345",
		CompanyName: "不適用公司", TaxID: "04595252",
	}
	if errs := member.Validate(); len(errs) != 0 || member.MobileBarcode != "" ||
		member.CompanyName != "" || member.TaxID != "" {
		t.Errorf("member invoice retained inapplicable company fields: %+v / %+v", member, errs)
	}
}

// TestValidateAsksForTheDestinationTheMethodNeeds covers each destination with
// its own fields and with the OTHER destination's, which is what a customer who
// filled one section and then switched methods submits.
func TestValidateAsksForTheDestinationTheMethodNeeds(t *testing.T) {
	contact := func(a *order.Delivery) {
		a.Email, a.RecipientName, a.Phone = "who@example.com", "王小明", "0912345678"
	}
	address := func(a *order.Delivery) {
		a.PostalCode, a.City, a.District, a.Street = "110", "台北市", "信義區", "松高路 1 號"
	}
	pickupAddress := func(a *order.Delivery) {
		a.PickupChain, a.PickupStoreCode, a.PickupStoreName = "seven_eleven", "123456", "信義門市"
	}
	chainOnly := func(a *order.Delivery) { a.PickupChain = "seven_eleven" }

	cases := []struct {
		name  string
		to    destination.Kind
		fill  []func(*order.Delivery)
		wants []string // the fields that must be reported, and no others
	}{
		{
			name: "an address order with an address",
			to:   destination.Address,
			fill: []func(*order.Delivery){contact, address},
		},
		{
			name: "a pickup order with a store",
			to:   destination.PickupPoint,
			fill: []func(*order.Delivery){contact, pickupAddress},
		},
		{
			name:  "an address order carrying only a store",
			to:    destination.Address,
			fill:  []func(*order.Delivery){contact, pickupAddress},
			wants: []string{"postal_code", "city", "district", "street"},
		},
		{
			name: "a pickup order carrying the chain alone",
			to:   destination.PickupPoint,
			fill: []func(*order.Delivery){contact, chainOnly},
		},
		{
			name:  "a pickup order carrying only an address",
			to:    destination.PickupPoint,
			fill:  []func(*order.Delivery){contact, address},
			wants: []string{"pickup_chain"},
		},
		{
			name:  "a method whose destination is unknown here",
			to:    destination.Kind("depot"),
			fill:  []func(*order.Delivery){contact, address, pickupAddress},
			wants: []string{"shipping"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &order.Delivery{To: c.to}
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

// The store codes below are REAL, read from ECPay's own GetStoreList on
// 2026-08-06: Hi-Life numbers its stores in four characters and 149 of its 1,350
// lead with a letter, so S884 must stay red under a digits-only rule.
func TestAPickupStoreCodeIsWhateverTheChainNumbersItsStores(t *testing.T) {
	cases := []struct {
		name   string
		chain  pickup.Chain
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
			a := &order.Delivery{
				To:              destination.PickupPoint,
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

// TestPickupStoreCodeAndNameArePairedOrNeither locks the tightened shape of
// order_private_data_pickup_complete at the form: once the picker writes one
// of the store code or store name, checkout must refuse a submission that
// does not also carry the other, rather than let the database be the first
// thing to say so.
func TestPickupStoreCodeAndNameArePairedOrNeither(t *testing.T) {
	t.Parallel()

	base := order.Delivery{
		To: destination.PickupPoint, Email: "who@example.com", RecipientName: "王小明", Phone: "0912345678",
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

// TestForDestinationDropsTheOtherHalf. Validation passes on a submission
// carrying both, so this is what stops the pair reaching
// order_private_data_one_destination.
func TestForDestinationDropsTheOtherHalf(t *testing.T) {
	both := func(to destination.Kind) *order.Delivery {
		return &order.Delivery{
			To: to, Email: "e@example.com", RecipientName: "n", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			PickupChain: "seven_eleven", PickupStoreCode: "123456", PickupStoreName: "信義門市",
		}
	}

	a := both(destination.PickupPoint)
	a.DropOtherDestination()
	if a.Street != "" || a.City != "" || a.District != "" || a.PostalCode != "" {
		t.Errorf("a pickup order kept an address: %q %q %q %q",
			a.PostalCode, a.City, a.District, a.Street)
	}
	if a.PickupStoreCode != "123456" {
		t.Errorf("the pickup point was dropped: %q", a.PickupStoreCode)
	}

	b := both(destination.Address)
	b.DropOtherDestination()
	if b.PickupChain != "" || b.PickupStoreCode != "" || b.PickupStoreName != "" {
		t.Errorf("an address order kept a pickup point: %q %q %q",
			b.PickupChain, b.PickupStoreCode, b.PickupStoreName)
	}
	if b.Street != "松高路 1 號" {
		t.Errorf("the address was dropped: %q", b.Street)
	}
}

// TestDestinationForRefusesWhatItDoesNotKnow. A method added to
// shipping_methods with an unknown destination must not collect a street.
func TestDestinationForRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, kind := range []string{"address", "pickup_point"} {
		if d, ok := destination.For(kind); !ok || string(d) != kind {
			t.Errorf("destination.For(%q) = %q, %v; want it recognised", kind, d, ok)
		}
	}
	for _, kind := range []string{"", "depot", "Address", "pickup"} {
		if d, ok := destination.For(kind); ok {
			t.Errorf("destination.For(%q) = %q, true; want it refused", kind, d)
		}
	}
}

// TestFillFromBookPrefersTheNamedAddressAndFallsBackToTheDefault holds both
// halves: the named address wins, and anything else — a guess, a stale
// bookmark, an address since deleted — starts from the default.
func TestFillFromBookPrefersTheNamedAddressAndFallsBackToTheDefault(t *testing.T) {
	t.Parallel()

	book := []pages.SavedAddress{
		{ID: "first", Label: "家", Name: "王小明", Phone: "0911111111",
			PostalCode: "106", City: "台北市", District: "大安區", Street: "和平東路 1 號", Default: true},
		{ID: "second", Label: "公司", Name: "王小明", Phone: "0222222222",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 2 號"},
	}

	for _, tt := range []struct{ name, wanted, street, chosen string }{
		{"nothing named", "", "和平東路 1 號", "first"},
		{"the second one", "second", "松高路 2 號", "second"},
		{"an id that names nothing", "deleted-or-guessed", "和平東路 1 號", "first"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := pages.CheckoutView{SavedAddresses: book}
			var addr order.Delivery
			fillFromBook(&view, &addr, tt.wanted)
			if addr.Street != tt.street {
				t.Errorf("filled from %q, want %q", addr.Street, tt.street)
			}
			if view.ChosenAddress != tt.chosen {
				t.Errorf("marked %q as chosen, want %q", view.ChosenAddress, tt.chosen)
			}
		})
	}
}

// TestFillFromBookLeavesAnEmptyBookAlone — a guest gets the form they always got.
func TestFillFromBookLeavesAnEmptyBookAlone(t *testing.T) {
	t.Parallel()
	view := pages.CheckoutView{}
	addr := order.Delivery{Street: "typed by hand"}
	fillFromBook(&view, &addr, "anything")
	if addr.Street != "typed by hand" || view.ChosenAddress != "" {
		t.Errorf("an empty book changed the form: %q / %q", addr.Street, view.ChosenAddress)
	}
}

// TestTheQuoteAddsTheSurchargeAfterTheThreshold holds the order the two parts of
// a delivery charge are combined in: folding the surcharge into the free-over
// test would make a large order to the outlying islands free to send.
func TestTheQuoteAddsTheSurchargeAfterTheThreshold(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		fee       int64
		surcharge int64
		want      int64
	}{
		{"a mainland order pays the fee", 8000, 0, 8000},
		{"an offshore order pays both", 8000, 20000, 28000},
		{"free shipping still pays the crossing", 0, 20000, 20000},
		{"free shipping to the mainland pays nothing", 0, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := Quote{FeeCents: tt.fee, Surcharge: tt.surcharge}
			got, err := q.Total()
			if err != nil {
				t.Fatalf("Total(): %v", err)
			}
			if got != tt.want {
				t.Errorf("Total() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInvoicePreferenceNormalizesOnlyItsOwnFields(t *testing.T) {
	donation := Invoice{Type: invoice.PreferenceDonate, DonationCode: " 00123 ", MobileBarcode: "/ABC+123", TaxID: "04595252", CompanyName: "Company"}
	if errs := donation.Validate(); len(errs) != 0 || donation.DonationCode != "00123" || donation.MobileBarcode != "" || donation.TaxID != "" || donation.CompanyName != "" {
		t.Fatalf("donation normalization: %+v / %+v", donation, errs)
	}
	bad := Invoice{Type: invoice.PreferenceDonate, DonationCode: "12A"}
	if errs := bad.Validate(); len(errs) != 1 || errs[0].Field != "invoice_donation_code" {
		t.Fatalf("invalid donation code errors=%+v", errs)
	}
}

func TestCheckoutRefusesPickupThatSkippedTheMapWhateverTheDeployment(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, addr := range map[string]*order.Delivery{
		"hi_life":  {To: destination.PickupPoint, PickupChain: "hi_life", PickupStoreCode: "012345", PickupStoreName: "門市"},
		"ok_mart":  {To: destination.PickupPoint, PickupChain: "ok_mart"},
		"no store": {To: destination.PickupPoint, PickupChain: "seven_eleven"},
	} {
		got := checkoutErrors(ctx, addr, nil, &Invoice{})
		if got["pickup_chain"] == "" && got["pickup_store"] == "" {
			t.Errorf("%s: a pickup order that skipped the map was not refused: %v", name, got)
		}
	}
}

func TestFullWidthDigitsAreFoldedBeforeTheCheckoutFieldsAreChecked(t *testing.T) {
	addr := order.Delivery{
		To: destination.Address, Email: "a@example.com", RecipientName: "王小明",
		Phone: "０９１２３４５６７８", PostalCode: "１１０",
		City: "台北市", District: "信義區", Street: "市府路1號",
	}
	addr.Trim()
	if addr.Phone != "0912345678" || addr.PostalCode != "110" {
		t.Errorf("Trim kept phone %q postal code %q, want the ASCII forms", addr.Phone, addr.PostalCode)
	}
	for _, e := range addr.Validate() {
		if e.Field == "phone" || e.Field == "postal_code" {
			t.Errorf("full-width input refused: %s %v", e.Field, e.MessageKey)
		}
	}

	inv := Invoice{Type: invoice.PreferenceMobile, MobileBarcode: "／ＡＢＣ＋１２３"}
	if errs := inv.Validate(); len(errs) != 0 || inv.MobileBarcode != "/ABC+123" {
		t.Errorf("barcode %q errors %v, want the folded barcode accepted", inv.MobileBarcode, errs)
	}
	company := Invoice{Type: invoice.PreferenceCompany, TaxID: "１２３４５６７８", CompanyName: "公司"}
	company.Validate()
	if company.TaxID != "12345678" {
		t.Errorf("tax ID = %q, want the ASCII digits", company.TaxID)
	}
}

// TestFillFromBookLeavesTheFormToTheShopperForAnotherAddress: 「其他地址」 is none
// of the saved ones, so the book fills nothing and the choice is remembered.
func TestFillFromBookLeavesTheFormToTheShopperForAnotherAddress(t *testing.T) {
	t.Parallel()
	view := pages.CheckoutView{SavedAddresses: []pages.SavedAddress{
		{ID: "first", Name: "王小明", Street: "和平東路 1 號", Default: true},
	}}
	addr := order.Delivery{Street: "typed by hand"}
	fillFromBook(&view, &addr, pages.OtherAddress)
	if addr.Street != "typed by hand" || addr.RecipientName != "" {
		t.Errorf("another address filled the form from the book: %+v", addr)
	}
	if view.ChosenAddress != pages.OtherAddress {
		t.Errorf("the choice was %q, want %q", view.ChosenAddress, pages.OtherAddress)
	}
}

// TestTheRecipientBoxIsAnExplicitRequestAndUntickingRestores holds the rule for
// 「收件人同會員資料」: ticking puts the account's name and phone in the fields
// even over other text, remembering what was there, and unticking puts that
// back into a field that still holds the account's value.
func TestTheRecipientBoxIsAnExplicitRequestAndUntickingRestores(t *testing.T) {
	t.Parallel()
	profile := pages.CheckoutProfile{Email: "me@example.com", Name: "王小明", Phone: "0912345678"}

	type fields struct{ name, phone string }
	for _, tt := range []struct {
		name       string
		checked    bool
		in         order.Delivery
		prev       fields
		want       fields
		wantPrev   fields
		wantTicked bool
	}{
		{"ticking fills an empty form", true, order.Delivery{},
			fields{}, fields{"王小明", "0912345678"}, fields{}, true},
		{"ticking overwrites typed text and remembers it", true,
			order.Delivery{RecipientName: "林小美", Phone: "0987654321"}, fields{},
			fields{"王小明", "0912345678"}, fields{"林小美", "0987654321"}, true},
		{"ticking over the account's own values keeps what was remembered", true,
			order.Delivery{RecipientName: "王小明", Phone: "0912345678"}, fields{"林小美", "0987654321"},
			fields{"王小明", "0912345678"}, fields{"林小美", "0987654321"}, true},
		{"unticking restores what was there before the tick", false,
			order.Delivery{RecipientName: "王小明", Phone: "0912345678"}, fields{"林小美", "0987654321"},
			fields{"林小美", "0987654321"}, fields{}, false},
		{"unticking clears when nothing was there", false,
			order.Delivery{RecipientName: "王小明", Phone: "0912345678"}, fields{},
			fields{}, fields{}, false},
		{"unticking leaves what was typed since", false,
			order.Delivery{RecipientName: "林小美", Phone: "0987654321"}, fields{"陳大文", "0911111111"},
			fields{"林小美", "0987654321"}, fields{"陳大文", "0911111111"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := pages.CheckoutView{
				Profile: profile, RecipientMe: tt.checked,
				RecipientPrevName: tt.prev.name, RecipientPrevPhone: tt.prev.phone,
			}
			addr := tt.in
			applyRecipient(&view, &addr)
			if (fields{addr.RecipientName, addr.Phone}) != tt.want {
				t.Errorf("fields = %q/%q, want %q/%q", addr.RecipientName, addr.Phone, tt.want.name, tt.want.phone)
			}
			if (fields{view.RecipientPrevName, view.RecipientPrevPhone}) != tt.wantPrev {
				t.Errorf("remembered %q/%q, want %q/%q", view.RecipientPrevName, view.RecipientPrevPhone, tt.wantPrev.name, tt.wantPrev.phone)
			}
			if view.RecipientMe != tt.wantTicked {
				t.Errorf("box ticked = %v, want %v", view.RecipientMe, tt.wantTicked)
			}
		})
	}
}

// TestAProfileWithoutAPhoneNeverWipesOne: an account with a name and no phone
// fills the name and leaves the phone alone, and still counts as the member.
func TestAProfileWithoutAPhoneNeverWipesOne(t *testing.T) {
	t.Parallel()
	view := pages.CheckoutView{
		Profile:     pages.CheckoutProfile{Email: "me@example.com", Name: "王小明"},
		RecipientMe: true,
	}
	addr := order.Delivery{RecipientName: "林小美", Phone: "0987654321"}
	applyRecipient(&view, &addr)
	if addr.RecipientName != "王小明" || addr.Phone != "0987654321" || !view.RecipientMe {
		t.Errorf("got %q/%q ticked=%v", addr.RecipientName, addr.Phone, view.RecipientMe)
	}
}

func TestPrefillRecipientNeverOverwritesAndReportsWhetherItIsTheMember(t *testing.T) {
	t.Parallel()
	profile := pages.CheckoutProfile{Email: "me@example.com", Name: "王小明", Phone: "0912345678"}

	empty := order.Delivery{}
	view := pages.CheckoutView{Profile: profile}
	prefillRecipient(&view, &empty)
	if empty.RecipientName != "王小明" || empty.Phone != "0912345678" || !view.RecipientMe {
		t.Errorf("an empty form was not filled from the account: %+v me=%v", empty, view.RecipientMe)
	}

	// A saved address for somebody else keeps its own recipient.
	gift := order.Delivery{RecipientName: "林小美", Phone: "0987654321"}
	view = pages.CheckoutView{Profile: profile}
	prefillRecipient(&view, &gift)
	if gift.RecipientName != "林小美" || gift.Phone != "0987654321" || view.RecipientMe {
		t.Errorf("the account overwrote another recipient: %+v me=%v", gift, view.RecipientMe)
	}

	// A guest has no profile, so nothing is filled and no box is checked.
	guest := order.Delivery{}
	view = pages.CheckoutView{}
	prefillRecipient(&view, &guest)
	if guest.RecipientName != "" || view.RecipientMe {
		t.Errorf("a guest was prefilled: %+v me=%v", guest, view.RecipientMe)
	}
}

// TestABlankCityIsAskedToBeFilledIn holds the wording of a free-text field: the
// city and district are typed, so neither is asked to be chosen.
func TestABlankCityIsAskedToBeFilledIn(t *testing.T) {
	t.Parallel()

	addr := order.Delivery{To: destination.Address, PostalCode: "110", District: "信義區", Street: "松高路 1 號"}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var got string
	for _, e := range addr.Validate() {
		if e.Field == "city" {
			got = i18n.T(ctx, e.MessageKey)
		}
	}
	if got != "請填寫縣市" {
		t.Errorf("a blank city is refused with %q, want 請填寫縣市", got)
	}
}

// TestTheCartSpeaksOfFreeDeliveryOnlyWhereItIsTrueForEveryMethod holds the cart's
// line to what checkout charges: one amount at which every method on offer turns
// free, or nothing.
func TestTheCartSpeaksOfFreeDeliveryOnlyWhereItIsTrueForEveryMethod(t *testing.T) {
	t.Parallel()

	home := pages.ShippingChoice{FeeCents: 100, FreeOverCents: 300000}
	pickupPoint := pages.ShippingChoice{FeeCents: 60, FreeOverCents: 150000}
	never := pages.ShippingChoice{FeeCents: 100}
	free := func(c pages.ShippingChoice) pages.ShippingChoice { c.FeeCents, c.Free = 0, true; return c }

	islands := func(c pages.ShippingChoice) pages.ShippingChoice { c.SurchargeZones = []string{"離島"}; return c }

	for _, tt := range []struct {
		name     string
		choices  []pages.ShippingChoice
		subtotal int64
		want     pages.FreeDelivery
	}{
		{"nothing offered", nil, 0, pages.FreeDelivery{}},
		{"one method, short", []pages.ShippingChoice{home}, 100000,
			pages.FreeDelivery{Kind: pages.FreeDeliveryShort, ShortfallCents: 200000, ThresholdCents: 300000}},
		{"two thresholds, the higher one is the one that is true for both",
			[]pages.ShippingChoice{home, pickupPoint}, 100000,
			pages.FreeDelivery{Kind: pages.FreeDeliveryShort, ShortfallCents: 200000, ThresholdCents: 300000}},
		{"one method is already free, the other is not",
			[]pages.ShippingChoice{home, free(pickupPoint)}, 200000,
			pages.FreeDelivery{Kind: pages.FreeDeliveryShort, ShortfallCents: 100000, ThresholdCents: 300000}},
		{"a method that is never free says nothing", []pages.ShippingChoice{home, never}, 100000, pages.FreeDelivery{}},
		{"every method free", []pages.ShippingChoice{free(home), free(pickupPoint)}, 300000,
			pages.FreeDelivery{Kind: pages.FreeDeliveryReached, ThresholdCents: 300000}},
		{"every method free, two of them charging the same zone extra",
			[]pages.ShippingChoice{islands(free(home)), islands(free(pickupPoint)), free(pickupPoint)}, 300000,
			pages.FreeDelivery{Kind: pages.FreeDeliveryReached, ThresholdCents: 300000, SurchargeZones: []string{"離島"}}},
	} {
		if got := freeDeliveryFor(tt.choices, tt.subtotal); !cmp.Equal(got, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
