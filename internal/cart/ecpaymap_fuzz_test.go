package cart

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/web"
)

// FuzzReadCallback: the map callback is a form any visitor can post, so what
// readCallback accepts is exactly the shape the checkout may carry on, and the
// redirect built from it can name nothing but the local checkout path.
func FuzzReadCallback(f *testing.F) {
	f.Add(aMerchantID, "UNIMART", "131386", "康是美", "台北市信義路1號", aNonce)
	f.Add(aMerchantID, "FAMI", "006598", "全家 店", "", aNonce)
	f.Add("other", "UNIMART", "1", "x", "", aNonce)
	f.Add(aMerchantID, "BOGUS", "1", "x", "", aNonce)
	f.Add(aMerchantID, "UNIMART", "a b", "x", "", aNonce)
	f.Add(aMerchantID, "UNIMART", "1", "\x00", "\n", "zz")
	f.Add(aMerchantID, "UNIMART", "1", strings.Repeat("店", 21), strings.Repeat("址", 81), aNonce)
	f.Add(aMerchantID, "UNIMART", "1", "//evil.example", "https://evil.example", aNonce)

	m, err := NewStoreMap(aMerchantID, string(ModeB2C), "", "https://goen.test")
	if err != nil {
		f.Fatalf("build the store map: %v", err)
	}

	f.Fuzz(func(t *testing.T, merchant, subtype, store, name, addr, nonce string) {
		form := url.Values{
			"MerchantID": {merchant}, "LogisticsSubType": {subtype}, "CVSStoreID": {store},
			"CVSStoreName": {name}, "CVSAddress": {addr}, "ExtraData": {nonce},
		}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout/store",
			strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		c, ok := m.readCallback(r)
		if !ok {
			return
		}
		if merchant != aMerchantID || !pickup.ValidStoreCode(c.Code) || !validNonce(c.Nonce) {
			t.Fatalf("accepted merchant %q code %q nonce %q", merchant, c.Code, c.Nonce)
		}
		if c.Name == "" || utf8.RuneCountInString(c.Name) > maxCallbackStoreNameRunes || web.HasControlChars(c.Name) {
			t.Fatalf("accepted store name %q", c.Name)
		}
		if utf8.RuneCountInString(c.Address) > maxCallbackAddressRunes || web.HasControlChars(c.Address) {
			t.Fatalf("accepted address %q", c.Address)
		}
		target, err := url.Parse(c.refreshTarget())
		if err != nil || target.Scheme != "" || target.Host != "" || target.Path != "/checkout" {
			t.Fatalf("the redirect %q leaves the local checkout path (%v)", c.refreshTarget(), err)
		}
		if got := target.Query().Get("pickup_store_name"); got != c.Name {
			t.Fatalf("the redirect carries store name %q, want %q", got, c.Name)
		}
	})
}
