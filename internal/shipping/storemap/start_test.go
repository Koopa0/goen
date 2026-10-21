package storemap

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
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

// aMapForm is the carrier's form as StartPage renders it.
func aMapForm() Form {
	return Form{
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

var (
	mapFormOpen = regexp.MustCompile(`(?s)<form[^>]*id="pickup-map-form".*?</form>`)
	inputName   = regexp.MustCompile(`<input[^>]*\bname="([^"]*)"`)
)

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestTheMapFormCarriesNothingTheShopperTyped is the PII guard. The checkout
// form is posted to goen's own start route, which keeps what was typed; the page
// that route answers is where the browser is handed to the carrier, and its form
// is the one that must hold nothing of the shopper's. Submitting the checkout
// form to the carrier itself would hand a third party the shopper's name, phone,
// e-mail and street address in one request.
func TestTheMapFormCarriesNothingTheShopperTyped(t *testing.T) {
	t.Parallel()

	html := renderToString(t, StartPage(aMapForm(), "/checkout?draft=1"))

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
