package pages

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProductJSONLDIsValidAndSaysWhatMatters proves the block parses and carries
// price, currency and availability. A malformed block is worse than none: a
// crawler that chokes on it may discard the page's other signals too.
func TestProductJSONLDIsValidAndSaysWhatMatters(t *testing.T) {
	v := &ProductView{
		Slug: "pixelight-9", Name: "Pixelight 9 5G", Summary: "旗艦手機",
		Brand: "Pixelight", SKU: "PXL-9-1-1",
		PriceCents: 2590000, Sellable: true,
		Rating: 4.5, RatingCount: 12,
	}
	doc := ProductJSONLD(v, "https://goen.example/")

	var got map[string]any
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("the document does not parse: %v\n%s", err, doc)
	}
	if got["@type"] != "Product" {
		t.Errorf("@type is %v, want Product", got["@type"])
	}
	if got["sku"] != "PXL-9-1-1" {
		t.Errorf("sku is %v", got["sku"])
	}

	offers, ok := got["offers"].(map[string]any)
	if !ok {
		t.Fatal("no offers block; a product with no offer is a page with no price")
	}
	// Exact decimal, from integer cents. 2590000 is NT$25,900.00 and nothing
	// near a float.
	if offers["price"] != "25900.00" {
		t.Errorf("price is %v, want the string 25900.00", offers["price"])
	}
	if offers["priceCurrency"] != "TWD" {
		t.Errorf("currency is %v, want TWD", offers["priceCurrency"])
	}
	if offers["availability"] != "https://schema.org/InStock" {
		t.Errorf("availability is %v, want schema.org's own InStock", offers["availability"])
	}
	// Absolute, and with the trailing slash of the base URL collapsed. A
	// relative url is one a crawler discards.
	if offers["url"] != "https://goen.example/p/pixelight-9" {
		t.Errorf("url is %v", offers["url"])
	}

	// Sold out says so, in the schema's vocabulary rather than the shop's.
	v.Sellable = false
	var out map[string]any
	if err := json.Unmarshal([]byte(ProductJSONLD(v, "https://goen.example")), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o := out["offers"].(map[string]any); o["availability"] != "https://schema.org/OutOfStock" {
		t.Errorf("a sold-out product reports %v", o["availability"])
	}
}

// TestJSONLDCannotEscapeItsScriptTag proves a product name cannot close the tag
// it is written into. The block goes into a <script> with templ.Raw, so
// anything closing the tag early would put attacker-controlled markup in the
// document; encoding/json is what prevents it, and swapping it for fmt.Sprintf
// would stop it being true.
func TestJSONLDCannotEscapeItsScriptTag(t *testing.T) {
	v := &ProductView{
		Slug: "x", Brand: "b", SKU: "s", PriceCents: 100,
		Name: `</script><script>alert(1)</script>`,
	}
	doc := ProductJSONLD(v, "https://goen.example")

	if strings.Contains(strings.ToLower(doc), "</script") {
		t.Errorf("the document can close its own tag:\n%s", doc)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The name still round-trips: escaping must not corrupt the value.
	if got["name"] != v.Name {
		t.Errorf("name came back as %q, want %q", got["name"], v.Name)
	}
}

// TestDollarsIsExact. Money through a float is how a price ends in .9999999.
func TestDollarsIsExact(t *testing.T) {
	tests := []struct {
		cents int64
		want  string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{99, "0.99"},
		{100, "1.00"},
		{2590000, "25900.00"},
		{2590050, "25900.50"},
		{10000000000, "100000000.00"},
	}
	for _, tt := range tests {
		if got := dollars(tt.cents); got != tt.want {
			t.Errorf("dollars(%d) = %q, want %q", tt.cents, got, tt.want)
		}
	}
}
