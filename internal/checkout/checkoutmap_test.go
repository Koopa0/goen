package checkout

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/i18n"
)

// aCheckoutWithAStore is the checkout as it renders after a shopper has come
// back from the carrier's map.
func aCheckoutWithAStore() *View {
	return &View{
		Cart:         cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []cart.ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupChains: pickupChains(),
		Address: Address{
			Email: "someone@goen.test", Name: "王小明", Phone: "0912345678",
			PickupChain: shipping.ChainSevenEleven, PickupStoreCode: "131386",
			PickupStoreName: "南港園區",
		},
		PickupStoreAddr: "台北市南港區三重路19-2號",
		PickupNonce:     "0123456789abcdef0123",
		MapOffered:      true,
	}
}

var (
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

// TestPageSwapRegionStoreButton: The region an in-place choice swaps carries
// the button that opens the store map, so 「選擇門市」 survives changing a chooser.
// It is the mutation checkoutChoiceSwap exists for: an in-place choice on the
// checkout page selects and replaces exactly the element hx-select names, and
// if that element does not also carry the button that opens the map, 「選擇門市」 is
// gone once the shopper has changed any chooser in place with scripting on.
func TestPageSwapRegionStoreButton(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), aCheckoutWithAStore()))

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

// TestPageStoreButtonSubmitsToGoen: Page puts the store button inside the
// checkout form and submits it to goen's own start route, and the page carries
// none of the carrier's form. It holds the structure that replaced the sibling
// form. The button sits inside the checkout form, so what was typed travels
// with it, and its formaction is goen's own route: the carrier is only ever
// reached from the page that route answers.
func TestPageStoreButtonSubmitsToGoen(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), aCheckoutWithAStore()))

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

// TestPageStoreSummary: Page shows the chosen store's name, code and address
// before the place-order button, the only control against a substituted store,
// and carries the store back to placement without posting the display-only
// address. It is a SECURITY test, not a layout one. The callback carries no
// signature and the map is run by the chain, so the shopper reading both before
// paying is the only control goen has against a substituted store. A change
// that reduces this to the name alone removes that control in silence.
func TestPageStoreSummary(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

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

// TestPageNoStoreOffersMap: Page offers the map and nothing else when no store
// has been chosen: no change-store control and no store fields. It is the first
// half of R10.
func TestPageNoStoreOffersMap(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

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

// TestPageRefusedStoreNotEchoed: Page never echoes a refused store: it says a
// store could not be confirmed and repeats nothing that arrived. It holds the
// second half of R1: the refusal says a store could not be confirmed and
// repeats nothing that arrived, because what arrived came from a cross-site
// post anybody can send.
func TestPageRefusedStoreNotEchoed(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	v.PickupRefused = true
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

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

// TestPageRefusedStoreAmongChains: With no chain chosen, Page shows a refused
// store's message once, above the chain choices, and draws no store box. With
// no chain chosen there is no map button, so the refusal must sit with the
// chains, above them, where the shopper's next step is.
func TestPageRefusedStoreAmongChains(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.Address.PickupChain = ""
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	v.MapOffered = false
	v.PickupRefused = true
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

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

// TestPageHostileStoreName: Page renders a store name that carries markup
// escaped, never as markup. It is the rendering half of the same worry: the
// carrier's page can report any text at all.
func TestPageHostileStoreName(t *testing.T) {
	t.Parallel()

	const hostile = `"><script>alert(1)</script>`
	v := aCheckoutWithAStore()
	v.Address.PickupStoreName = hostile
	v.PickupStoreAddr = hostile
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("a store name reached the page as markup")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("the store name was not rendered at all, so this proves nothing")
	}
}

// TestPageNoMapOffered: With no map offered, Page renders no store-map form,
// carrier address or choose-store control, and still asks for a chain. It is
// the unconfigured deployment: the checkout asks for a chain and behaves
// exactly as it did before the map existed.
func TestPageNoMapOffered(t *testing.T) {
	t.Parallel()

	v := aCheckoutWithAStore()
	v.MapOffered = false
	v.Address.PickupStoreCode, v.Address.PickupStoreName = "", ""
	v.PickupStoreAddr = ""
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))

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
