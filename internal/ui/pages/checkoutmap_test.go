package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
)

// ecpayMapFields is every request parameter ECPay's 門市電子地圖 documents. It is
// an allow-list and not a description: the guard below refuses any input the
// map form grows that is not on it, because the failure it exists to catch is a
// future change that lets the shopper's name, phone, e-mail or street address
// ride along to a third party.
var ecpayMapFields = map[string]bool{
	"MerchantID":       true,
	"MerchantTradeNo":  true,
	"LogisticsType":    true,
	"LogisticsSubType": true,
	"IsCollection":     true,
	"ServerReplyURL":   true,
	"ExtraData":        true,
	// Sent only to a phone, and only 7-ELEVEN reads it.
	"Device": true,
}

// aCheckoutWithAStore is the checkout as it renders after a shopper has come
// back from the carrier's map.
func aCheckoutWithAStore() *CheckoutView {
	return &CheckoutView{
		Cart:         CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupBrands: CheckoutPickupBrandChoices(),
		Address: CheckoutAddress{
			Email: "someone@goen.test", Name: "王小明", Phone: "0912345678",
			PickupBrand: pickup.SevenEleven, PickupStoreCode: "131386",
			PickupStoreName: "南港園區",
		},
		PickupStoreAddr: "台北市南港區三重路19-2號",
		PickupNonce:     "0123456789abcdef0123",
		Map: CheckoutMapForm{
			Action:          "https://logistics-stage.ecpay.com.tw/Express/map",
			MerchantID:      "1000001",
			MerchantTradeNo: "ABCDEFGHIJ1234567890",
			LogisticsType:   "CVS", LogisticsSubType: "UNIMART",
			IsCollection:   "N",
			ServerReplyURL: "https://goen.test/checkout/pickup/return",
			ExtraData:      "0123456789abcdef0123",
			Device:         "1",
		},
	}
}

var (
	mapFormOpen  = regexp.MustCompile(`(?s)<form[^>]*id="pickup-map-form".*?</form>`)
	inputName    = regexp.MustCompile(`<input[^>]*\bname="([^"]*)"`)
	associatedEl = regexp.MustCompile(`<[^>]*\bform="pickup-map-form"[^>]*>`)
	// hxSelectAttr pulls the swap's own selector out of a chooser's attributes,
	// rather than a test hardcoding the id it currently names — so a mutation
	// of hx-select is what this file's own mutation test flips red.
	hxSelectAttr = regexp.MustCompile(`hx-select="#([^"]+)"`)
	// tagName reads the element name off an opening tag, so elementByID can
	// balance the SAME tag hx-select actually named. hx-select can point at a
	// <div> (the fix) or a <form> (the bug, under mutation) and the two must
	// not be balanced against each other: #checkout-form's own </form> closes
	// well before the sibling map form, while #checkout-region's </div> closes
	// after it.
	tagName = regexp.MustCompile(`^<([a-zA-Z0-9]+)`)
)

// elementByID returns the outerHTML of the element carrying id="id", found by
// reading the tag name off its own opening tag and then counting THAT tag's
// open and close markers onward. Generic on purpose: the element hx-select
// names is exactly what a browser's own selector would find, whatever element
// it turns out to be.
func elementByID(t *testing.T, html, id string) string {
	t.Helper()

	marker := `id="` + id + `"`
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatalf("no element carries id=%q", id)
	}
	start := strings.LastIndex(html[:at], "<")
	openEnd := strings.Index(html[start:], ">")
	if start < 0 || openEnd < 0 {
		t.Fatalf("id=%q is not inside a tag", id)
	}
	name := tagName.FindStringSubmatch(html[start : start+openEnd+1])
	if name == nil {
		t.Fatalf("id=%q is not on a recognisable tag", id)
	}
	tag := regexp.MustCompile(`</?` + name[1] + `\b[^>]*>`)
	depth := 0
	for _, m := range tag.FindAllStringIndex(html[start:], -1) {
		found := html[start+m[0] : start+m[1]]
		if strings.HasPrefix(found, "</") {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return html[start : start+m[1]]
		}
	}
	t.Fatalf("id=%q is never closed", id)
	return ""
}

// TestTheSwapRegionCarriesBothForms is the mutation checkoutChoiceSwap exists
// for: an in-place choice on the checkout page selects and replaces exactly
// the element hx-select names, and if that element does not also carry the
// map form and the button that submits it, 「選擇門市」 has nothing to submit
// once the shopper has changed any chooser in place with scripting on.
func TestTheSwapRegionCarriesBothForms(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), aCheckoutWithAStore()))

	chooser := tagCarrying(t, html, `hx-vals="{&#34;update&#34;:&#34;shipping&#34;}"`)
	m := hxSelectAttr.FindStringSubmatch(chooser)
	if m == nil {
		t.Fatal("the shipping chooser carries no hx-select")
	}
	region := elementByID(t, html, m[1])

	if !strings.Contains(region, `id="pickup-map-form"`) {
		t.Errorf("the region hx-select names (id=%q) does not carry the map form; "+
			"an in-place choice swaps it away and 「選擇門市」 has nothing to submit", m[1])
	}
	if !strings.Contains(region, `form="pickup-map-form"`) {
		t.Errorf("the region hx-select names (id=%q) does not carry the button that "+
			"submits the map form", m[1])
	}
}

// TestTheMapFormCarriesNothingTheShopperTyped is the PII guard, and it is the
// reason the map form is a sibling rather than a formaction. Submitting
// #checkout-form to the carrier would hand a third party the shopper's name,
// phone, e-mail and street address in one request.
func TestTheMapFormCarriesNothingTheShopperTyped(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	form := mapFormOpen.FindString(html)
	if form == "" {
		t.Fatal("no map form rendered; the rest of this test proves nothing")
	}

	names := inputName.FindAllStringSubmatch(form, -1)
	if len(names) < 7 {
		t.Fatalf("the map form carries %d inputs; ECPay requires seven", len(names))
	}
	for _, m := range names {
		if !ecpayMapFields[m[1]] {
			t.Errorf("the map form carries %q, which is not a parameter ECPay's map "+
				"documents — anything goen adds here is sent to a third party", m[1])
		}
	}
	for _, typed := range []string{
		"email", "name", "phone", "postal_code", "city", "district", "street",
		"note", "coupon", "invoice_carrier", "invoice_tax_id", "checkout_quote",
		"idempotency", "pickup_n",
	} {
		if strings.Contains(form, `name="`+typed+`"`) {
			t.Errorf("the map form carries %q, which belongs to goen and to nobody else", typed)
		}
	}
	// A shopper's own address must not reach the carrier even as a value.
	for _, secret := range []string{"someone@goen.test", "王小明", "0912345678"} {
		if strings.Contains(form, secret) {
			t.Errorf("the map form carries %q", secret)
		}
	}
}

// TestTheMapFormIsNotInsideTheCheckoutForm holds the structure the HTML parser
// requires: a <form> nested in a <form> is dropped outright, so "sibling" is
// not a style preference.
func TestTheMapFormIsNotInsideTheCheckoutForm(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), aCheckoutWithAStore()))

	open := strings.Index(html, `id="checkout-form"`)
	if open < 0 {
		t.Fatal("no checkout form rendered")
	}
	end := strings.Index(html[open:], "</form>")
	if end < 0 {
		t.Fatal("the checkout form is never closed")
	}
	inside := html[open : open+end]
	if strings.Contains(inside, `id="pickup-map-form"`) {
		t.Error("the map form is inside the checkout form; the parser drops the inner " +
			"one and the button silently posts the whole checkout to goen instead")
	}
	if !strings.Contains(html, `id="pickup-map-form"`) {
		t.Error("the map form is not on the page at all")
	}
	if !strings.Contains(inside, `form="pickup-map-form"`) {
		t.Error("the button that opens the map is not in the pickup section")
	}
}

// TestTheMapButtonContributesNoFieldOfItsOwn is the mechanic the security
// review named: a form-associated submit button contributes its OWN name and
// value to the form it submits, so one with a name is one more field on its way
// to a third party.
func TestTheMapButtonContributesNoFieldOfItsOwn(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), aCheckoutWithAStore()))

	found := associatedEl.FindAllString(html, -1)
	if len(found) == 0 {
		t.Fatal("nothing is associated with the map form, so nothing submits it")
	}
	for _, el := range found {
		if strings.Contains(el, " name=") {
			t.Errorf("%s carries a name, which rides along to the carrier", el)
		}
		if strings.Contains(el, "formaction") {
			t.Errorf("%s carries formaction; the checkout form must never post to "+
				"the carrier", el)
		}
	}
}

// TestTheStoreSummaryShowsTheNameAndTheAddress is a SECURITY test, not a
// layout one. The callback carries no signature and the map is run by the
// chain, so the shopper reading both before paying is the only control goen has
// against a substituted store. A change that reduces this to the name alone
// removes that control in silence.
func TestTheStoreSummaryShowsTheNameAndTheAddress(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	for _, want := range []string{
		v.Address.PickupStoreName,
		v.Address.PickupStoreCode,
		v.PickupStoreAddr,
		i18n.T(ctx, i18n.KeyPickupChangeStore),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the checkout does not show %q before the place-order button", want)
		}
	}
	// Carried back to placement, where the same nonce rule runs again.
	for _, want := range []string{
		`name="pickup_store_code" value="131386"`,
		`name="pickup_store_name" value="南港園區"`,
		`name="pickup_n" value="0123456789abcdef0123"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the form does not carry %s back to the order", want)
		}
	}
	// The address is display only: it has no column, and posting it would put
	// carrier-supplied text into a write.
	if strings.Contains(html, `name="pickup_store_addr"`) {
		t.Error("the store address is posted back; it is display only and is never stored")
	}
}

// TestNoStoreYetOffersTheMapAndNothingElse is the first half of R10.
func TestNoStoreYetOffersTheMapAndNothingElse(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	if !strings.Contains(html, i18n.T(ctx, i18n.KeyPickupChooseStore)) {
		t.Error("no control opens the carrier's map")
	}
	if strings.Contains(html, i18n.T(ctx, i18n.KeyPickupChangeStore)) {
		t.Error("the form offers to CHANGE a store that was never chosen")
	}
	for _, gone := range []string{`name="pickup_store_code"`, `name="pickup_store_name"`} {
		if strings.Contains(html, gone) {
			t.Errorf("the form carries %s with no store chosen", gone)
		}
	}
}

// TestARefusedStoreIsNeverEchoed holds the second half of R1: the refusal says
// a store could not be confirmed and repeats nothing that arrived, because what
// arrived came from a cross-site post anybody can send.
func TestARefusedStoreIsNeverEchoed(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	v.PickupRefused = true
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	if !strings.Contains(html, i18n.T(ctx, i18n.KeyPickupStoreUnconfirmed)) {
		t.Error("a refused store renders no message at all")
	}
	for _, forged := range []string{"南港園區", "131386", "台北市南港區三重路19-2號"} {
		if strings.Contains(html, forged) {
			t.Errorf("the refusal page echoes %q, which was posted by whoever sent the "+
				"request rather than chosen by this shopper", forged)
		}
	}
}

// TestAHostileStoreNameCannotBreakOutOfThePage is the rendering half of the
// same worry: the carrier's page can report any text at all.
func TestAHostileStoreNameCannotBreakOutOfThePage(t *testing.T) {
	t.Parallel()

	const hostile = `"><script>alert(1)</script>`
	v := aCheckoutWithAStore()
	v.Address.PickupStoreName = hostile
	v.PickupStoreAddr = hostile
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a store name reached the page as markup")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("the store name was not rendered at all, so this proves nothing")
	}
}

// TestNoMapNoButtonAndNoForm is the unconfigured deployment: the checkout asks
// for a chain and behaves exactly as it did before the map existed.
func TestNoMapNoButtonAndNoForm(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Map = CheckoutMapForm{}
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	for _, gone := range []string{
		"pickup-map-form", "logistics-stage.ecpay.com.tw",
		i18n.T(ctx, i18n.KeyPickupChooseStore),
	} {
		if strings.Contains(html, gone) {
			t.Errorf("an unconfigured goen renders %q", gone)
		}
	}
	if !strings.Contains(html, `name="pickup_brand"`) {
		t.Error("the chain chooser is gone too; unconfigured must mean unchanged")
	}
}
