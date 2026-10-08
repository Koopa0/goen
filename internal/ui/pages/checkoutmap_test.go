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

// aMapForm is the carrier's form as PickupStart renders it.
func aMapForm() CheckoutMapForm {
	return CheckoutMapForm{
		Action:          "https://logistics-stage.ecpay.com.tw/Express/map",
		MerchantID:      "1000001",
		MerchantTradeNo: "ABCDEFGHIJ1234567890",
		LogisticsType:   "CVS", LogisticsSubType: "UNIMART",
		IsCollection:   "N",
		ServerReplyURL: "https://goen.test/checkout/pickup/return",
		ExtraData:      "0123456789abcdef0123",
		Device:         "1",
	}
}

// aCheckoutWithAStore is the checkout as it renders after a shopper has come
// back from the carrier's map.
func aCheckoutWithAStore() *CheckoutView {
	return &CheckoutView{
		Cart:         CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupChains: CheckoutPickupChainChoices(),
		Address: CheckoutAddress{
			Email: "someone@goen.test", Name: "王小明", Phone: "0912345678",
			PickupChain: pickup.SevenEleven, PickupStoreCode: "131386",
			PickupStoreName: "南港園區",
		},
		PickupStoreAddr: "台北市南港區三重路19-2號",
		PickupNonce:     "0123456789abcdef0123",
		MapOffered:      true,
	}
}

var (
	mapFormOpen = regexp.MustCompile(`(?s)<form[^>]*id="pickup-map-form".*?</form>`)
	inputName   = regexp.MustCompile(`<input[^>]*\bname="([^"]*)"`)
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

// TestTheSwapRegionCarriesTheStoreButton is the mutation checkoutChoiceSwap
// exists for: an in-place choice on the checkout page selects and replaces
// exactly the element hx-select names, and if that element does not also carry
// the button that opens the map, 「選擇門市」 is gone once the shopper has
// changed any chooser in place with scripting on.
func TestTheSwapRegionCarriesTheStoreButton(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), aCheckoutWithAStore()))

	chooser := tagCarrying(t, html, `hx-vals="{&#34;update&#34;:&#34;shipping&#34;}"`)
	m := hxSelectAttr.FindStringSubmatch(chooser)
	if m == nil {
		t.Fatal("the shipping chooser carries no hx-select")
	}
	region := elementByID(t, html, m[1])

	if !strings.Contains(region, `formaction="`+PickupStartAction+`"`) {
		t.Errorf("the region hx-select names (id=%q) does not carry the button that "+
			"opens the map; an in-place choice swaps it away", m[1])
	}
}

// TestTheMapFormCarriesNothingTheShopperTyped is the PII guard. The checkout
// form is posted to goen's own start route, which keeps what was typed; the page
// that route answers is where the browser is handed to the carrier, and its form
// is the one that must hold nothing of the shopper's. Submitting the checkout
// form to the carrier itself would hand a third party the shopper's name, phone,
// e-mail and street address in one request.
func TestTheMapFormCarriesNothingTheShopperTyped(t *testing.T) {
	t.Parallel()

	html := renderToString(t, PickupStart(aMapForm(), "/checkout?draft=1"))

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
	// The hand-off page renders no page chrome, so a shopper's own address is
	// nowhere on it: not as a field and not as a value.
	for _, secret := range []string{"someone@goen.test", "王小明", "0912345678"} {
		if strings.Contains(html, secret) {
			t.Errorf("the hand-off page carries %q", secret)
		}
	}
	if !strings.Contains(html, "data-handoff") {
		t.Error("nothing marks the form for the script that submits it")
	}
}

// TestTheStoreButtonSubmitsToGoenAndNeverToTheCarrier holds the structure that
// replaced the sibling form. The button sits inside the checkout form, so what
// was typed travels with it, and its formaction is goen's own route: the
// carrier is only ever reached from the page that route answers.
func TestTheStoreButtonSubmitsToGoenAndNeverToTheCarrier(t *testing.T) {
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

	button := tagCarrying(t, inside, `formaction="`+PickupStartAction+`"`)
	if !strings.Contains(button, `formmethod="post"`) || !strings.Contains(button, "formnovalidate") {
		t.Errorf("%s must post, and without the browser's required-field check: nobody has finished", button)
	}
	// A submit button contributes its OWN name and value to the form it submits.
	if strings.Contains(button, " name=") || strings.Contains(button, " value=") {
		t.Errorf("%s carries a name or value, which is one more field in the submit", button)
	}
	for _, carrier := range []string{"pickup-map-form", "logistics-stage.ecpay.com.tw", "MerchantTradeNo"} {
		if strings.Contains(html, carrier) {
			t.Errorf("the checkout page carries %q; the carrier's form belongs on the hand-off page", carrier)
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

// TestARefusedStoreSaysTheNextStepAmongTheChains: with no chain chosen there is
// no map button, so the refusal must sit with the chains, above them, where the
// shopper's next step is.
func TestARefusedStoreSaysTheNextStepAmongTheChains(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupChain = ""
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	v.MapOffered = false
	v.PickupRefused = true
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), v))

	msg := i18n.T(ctx, i18n.KeyPickupStoreUnconfirmed)
	if got := strings.Count(html, msg); got != 1 {
		t.Fatalf("the refusal appears %d times, want 1", got)
	}
	chains := elementByID(t, html, "pickup_chain")
	at := strings.Index(chains, msg)
	if at < 0 {
		t.Fatal("the refusal is outside the chain choices")
	}
	if first := strings.Index(chains, `name="pickup_chain"`); at > first {
		t.Error("the refusal comes after the first chain choice, want above them")
	}
	if strings.Contains(html, `class="goen-pickup"`) {
		t.Error("a refusal with no chain draws the store box around nothing")
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
	v.MapOffered = false
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
	if !strings.Contains(html, `name="pickup_chain"`) {
		t.Error("the chain chooser is gone too; unconfigured must mean unchanged")
	}
}
