package cart

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/koopa0/goen/internal/ui/pages"
)

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

// TestAddressValidateRejects covers what the server must catch even though the
// browser was asked to catch it first. Each case is one field, so a failure
// names the rule that broke.
func TestAddressValidateRejects(t *testing.T) {
	t.Parallel()

	valid := Address{
		// The destination decides which half of the struct is required, so
		// every fixture here names one. A zero To is refused outright, which is
		// what stops a method nothing understands collecting a street address.
		To:    ToAddress,
		Email: "a@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	for _, tt := range []struct {
		name  string
		mut   func(*Address)
		field string
	}{
		{"no email", func(a *Address) { a.Email = "" }, "email"},
		{"email with no @", func(a *Address) { a.Email = "nope" }, "email"},
		{"email with no domain dot", func(a *Address) { a.Email = "a@example" }, "email"},
		{"email with a space", func(a *Address) { a.Email = "a b@example.com" }, "email"},
		{"no name", func(a *Address) { a.Name = "  " }, "name"},
		{"no phone", func(a *Address) { a.Phone = "" }, "phone"},
		{"phone with letters", func(a *Address) { a.Phone = "09abc12345" }, "phone"},
		{"phone too short", func(a *Address) { a.Phone = "12345" }, "phone"},
		{"postal code not digits", func(a *Address) { a.PostalCode = "11A" }, "postal_code"},
		{"postal code too short", func(a *Address) { a.PostalCode = "11" }, "postal_code"},
		{"no city", func(a *Address) { a.City = "" }, "city"},
		{"no district", func(a *Address) { a.District = "" }, "district"},
		{"no street", func(a *Address) { a.Street = "" }, "street"},
		// A newline in a name is how a header or a shipping label gets a second
		// line it was never given.
		{"newline in the name", func(a *Address) { a.Name = "王小明\nX" }, "name"},
		// C1 controls are invisible and are the classic way past a check that
		// only looked at ASCII.
		{"C1 control in the street", func(a *Address) { a.Street = "松高路\u0085 1 號" }, "street"},
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

// TestAddressValidateAccepts is the control: without it, a Validate that
// rejected everything would pass every case above.
func TestAddressValidateAccepts(t *testing.T) {
	t.Parallel()

	for _, a := range []Address{
		{To: ToAddress, Email: "a@example.com", Name: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"},
		{To: ToAddress, Email: "someone.long+tag@sub.example.co.uk", Name: "Li Hua", Phone: "+886 2 2700-1234",
			PostalCode: "10041", City: "台北市", District: "中正區", Street: "重慶南路一段 122 號",
			Note: "請放管理室"},
		// 超商取貨, which has no street at all.
		{To: ToPickupPoint, Email: "pick@example.com", Name: "陳小明", Phone: "0933444555",
			PickupBrand: "family_mart", PickupStoreCode: "012345", PickupStoreName: "台北車站門市"},
	} {
		if errs := a.Validate(); len(errs) != 0 {
			t.Errorf("a valid address was rejected: %+v", errs)
		}
	}
}

// TestValidateAsksForTheDestinationTheMethodNeeds is the rule that decides
// which half of the form is required.
//
// The cases below are the four combinations that matter: each destination with
// its own fields, and each with the OTHER destination's fields — which is what
// a customer who filled one section and then switched methods submits.
func TestValidateAsksForTheDestinationTheMethodNeeds(t *testing.T) {
	contact := func(a *Address) {
		a.Email, a.Name, a.Phone = "who@example.com", "王小明", "0912345678"
	}
	address := func(a *Address) {
		a.PostalCode, a.City, a.District, a.Street = "110", "台北市", "信義區", "松高路 1 號"
	}
	pickup := func(a *Address) {
		a.PickupBrand, a.PickupStoreCode, a.PickupStoreName = "seven_eleven", "123456", "信義門市"
	}

	cases := []struct {
		name  string
		to    Destination
		fill  []func(*Address)
		wants []string // the fields that must be reported, and no others
	}{
		{
			name: "an address order with an address",
			to:   ToAddress,
			fill: []func(*Address){contact, address},
		},
		{
			name: "a pickup order with a store",
			to:   ToPickupPoint,
			fill: []func(*Address){contact, pickup},
		},
		{
			// The store fields do not stand in for an address.
			name:  "an address order carrying only a store",
			to:    ToAddress,
			fill:  []func(*Address){contact, pickup},
			wants: []string{"postal_code", "city", "district", "street"},
		},
		{
			// Nor the other way round.
			name:  "a pickup order carrying only an address",
			to:    ToPickupPoint,
			fill:  []func(*Address){contact, address},
			wants: []string{"pickup_brand", "pickup_store_code", "pickup_store_name"},
		},
		{
			// A destination nothing recognises is refused rather than defaulted
			// to an address, which would let a method added later quietly
			// collect a street nobody can deliver to.
			name:  "a method whose destination is unknown here",
			to:    Destination("depot"),
			fill:  []func(*Address){contact, address, pickup},
			wants: []string{"shipping"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Address{To: c.to}
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

// TestForDestinationDropsTheOtherHalf. Validation passes on a submission
// carrying both — every field it looked at was filled — so this is what stops
// the pair reaching order_private_data_one_destination.
func TestForDestinationDropsTheOtherHalf(t *testing.T) {
	both := func(to Destination) *Address {
		return &Address{
			To: to, Email: "e@example.com", Name: "n", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			PickupBrand: "seven_eleven", PickupStoreCode: "123456", PickupStoreName: "信義門市",
		}
	}

	a := both(ToPickupPoint)
	a.ForDestination()
	if a.Street != "" || a.City != "" || a.District != "" || a.PostalCode != "" {
		t.Errorf("a pickup order kept an address: %q %q %q %q",
			a.PostalCode, a.City, a.District, a.Street)
	}
	if a.PickupStoreCode != "123456" {
		t.Errorf("the pickup point was dropped: %q", a.PickupStoreCode)
	}

	b := both(ToAddress)
	b.ForDestination()
	if b.PickupBrand != "" || b.PickupStoreCode != "" || b.PickupStoreName != "" {
		t.Errorf("an address order kept a pickup point: %q %q %q",
			b.PickupBrand, b.PickupStoreCode, b.PickupStoreName)
	}
	if b.Street != "松高路 1 號" {
		t.Errorf("the address was dropped: %q", b.Street)
	}
}

// TestDestinationForRefusesWhatItDoesNotKnow. Defaulting an unknown kind to an
// address is the failure this prevents: a method added to shipping_methods with
// a destination nothing here understands would silently collect a street.
func TestDestinationForRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, kind := range []string{"address", "pickup_point"} {
		if d, ok := DestinationFor(kind); !ok || string(d) != kind {
			t.Errorf("DestinationFor(%q) = %q, %v; want it recognised", kind, d, ok)
		}
	}
	for _, kind := range []string{"", "depot", "Address", "pickup"} {
		if d, ok := DestinationFor(kind); ok {
			t.Errorf("DestinationFor(%q) = %q, true; want it refused", kind, d)
		}
	}
}

// TestFillFromBookPrefersTheNamedAddressAndFallsBackToTheDefault holds both
// halves: the named address wins, and anything else starts from the default.
//
// The fallback matters as much as the choice: the id comes off a query string,
// so a guess, a stale bookmark or an address deleted since the page was opened
// all arrive here — and an error page in the middle of a checkout, over a field
// nobody typed, is worse than starting from the default.
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
			var addr Address
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

// TestFillFromBookLeavesAnEmptyBookAlone — a guest, or a customer who has never
// saved one, gets the form they always got.
func TestFillFromBookLeavesAnEmptyBookAlone(t *testing.T) {
	t.Parallel()
	view := pages.CheckoutView{}
	addr := Address{Street: "typed by hand"}
	fillFromBook(&view, &addr, "anything")
	if addr.Street != "typed by hand" || view.ChosenAddress != "" {
		t.Errorf("an empty book changed the form: %q / %q", addr.Street, view.ChosenAddress)
	}
}

// TestTheQuoteAddsTheSurchargeAfterTheThreshold holds the order the two parts
// of a delivery charge are combined in.
//
// The ordering is the commercial decision and the one that is easy to get
// backwards: folding the surcharge into the free-over test would make a large
// order to 金門 free to send, which it is not.
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
			if got := q.Total(); got != tt.want {
				t.Errorf("Total() = %d, want %d", got, tt.want)
			}
		})
	}
}
