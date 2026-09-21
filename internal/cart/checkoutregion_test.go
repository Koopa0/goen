package cart

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages"
)

// divTag walks a rendered page's div open and close tags, so
// checkoutRegionElement can pull one balanced <div> out of a full checkout
// render the way a browser's own DOM would.
var divTag = regexp.MustCompile(`</?div\b[^>]*>`)

// checkoutRegionElement returns the outerHTML of the element carrying
// id="checkout-region" — the element #checkout-region as an hx-select value
// names, and the one PlaceOrder's update branch must swap in whole, forms and
// all, for the store button to have anything to submit after an in-place
// choice.
//
// Deliberately div-only, unlike the pages package's own elementByID: this
// file always looks up the literal "checkout-region" id, which is only ever
// a <div> in cart.templ, rather than an id read off a live hx-select
// attribute that could name any element.
func checkoutRegionElement(t *testing.T, html string) string {
	t.Helper()

	marker := `id="checkout-region"`
	at := strings.Index(html, marker)
	if at < 0 {
		t.Fatal(`no element carries id="checkout-region"`)
	}
	start := strings.LastIndex(html[:at], "<div")
	if start < 0 {
		t.Fatal(`id="checkout-region" is not on a <div>`)
	}
	depth := 0
	for _, m := range divTag.FindAllStringIndex(html[start:], -1) {
		tag := html[start+m[0] : start+m[1]]
		if strings.HasPrefix(tag, "</div") {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return html[start : start+m[1]]
		}
	}
	t.Fatal(`id="checkout-region" is never closed`)
	return ""
}

// TestSwitchingTheChainInPlaceRefreshesTheMapForm holds the update path
// PlaceOrder takes when a chooser fires: r.PostFormValue("update") is set, so
// renderCheckout answers with the fresh render rather than placing an order.
// The shopper has switched the pickup radio from 7-ELEVEN to 全家; the
// response renderCheckout produces is exactly what #checkout-region selects
// and replaces, and it must carry 全家's own LogisticsSubType (FAMI, under the
// b2c contract) rather than a stale UNIMART left over from the chain just
// changed away from.
func TestSwitchingTheChainInPlaceRefreshesTheMapForm(t *testing.T) {
	t.Parallel()

	h := &Handler{
		storeMap: testMap(t, ModeB2C),
		secure:   true,
		log:      slog.New(slog.DiscardHandler),
	}

	// What checkoutSubmission would have built from this POST: everything
	// typed is echoed, and the chain just switched to 全家.
	view := pages.CheckoutView{
		Cart:         pages.CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []pages.ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupBrands: pages.CheckoutPickupBrandChoices(),
		Address: pages.CheckoutAddress{
			Email: "someone@goen.test", Name: "王小明", Phone: "0912345678",
			PickupBrand: pickup.FamilyMart,
		},
	}

	form := url.Values{"update": {"pickup_brand"}, "pickup_brand": {"family_mart"}}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	h.renderCheckout(rec, req, http.StatusOK, &view)

	region := checkoutRegionElement(t, rec.Body.String())
	if !strings.Contains(region, `id="pickup-map-form"`) {
		t.Fatal("the swap region carries no map form; the store button has nothing to submit")
	}
	if !strings.Contains(region, `form="pickup-map-form"`) {
		t.Error("the swap region carries no button naming the map form")
	}
	if !strings.Contains(region, `name="LogisticsSubType" value="FAMI"`) {
		t.Errorf("the map form does not carry 全家's own subtype (FAMI):\n%s", region)
	}
	if strings.Contains(region, `value="UNIMART"`) {
		t.Error("the map form still carries 7-ELEVEN's subtype after switching to 全家")
	}
}
