package product

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func matrix() []Variant {
	return []Variant{
		{ID: "1", SKU: "A-1-1", PriceCents: 3390000, Sellable: true, Available: 10,
			Options: map[string]string{"顏色": "星霧藍", "容量": "256GB"}},
		{ID: "2", SKU: "A-1-2", PriceCents: 3690000, Sellable: false,
			Options: map[string]string{"顏色": "星霧藍", "容量": "512GB"}},
		{ID: "3", SKU: "A-2-1", PriceCents: 3390000, Sellable: true, Available: 8,
			Options: map[string]string{"顏色": "曜石黑", "容量": "256GB"}},
		{ID: "4", SKU: "A-2-2", PriceCents: 3690000, Sellable: true, Available: 5,
			Options: map[string]string{"顏色": "曜石黑", "容量": "512GB"}},
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		sel       Selection
		wantSKU   string
		wantExact bool
	}{
		{
			name:      "nothing chosen quotes a buyable variant",
			sel:       Selection{},
			wantSKU:   "A-1-1",
			wantExact: false,
		},
		{
			name:      "one option chosen leaves the other open",
			sel:       Selection{"顏色": "星霧藍"},
			wantSKU:   "A-1-1",
			wantExact: false,
		},
		{
			name:      "both options pin one variant",
			sel:       Selection{"顏色": "曜石黑", "容量": "512GB"},
			wantSKU:   "A-2-2",
			wantExact: true,
		},
		{
			name:      "a sold-out combination still resolves exactly",
			sel:       Selection{"顏色": "星霧藍", "容量": "512GB"},
			wantSKU:   "A-1-2",
			wantExact: true,
		},
		{
			name:      "a partial choice prefers a variant that can be bought",
			sel:       Selection{"容量": "512GB"},
			wantSKU:   "A-2-2",
			wantExact: false,
		},
	}

	// The matrix above cannot tell "first buyable" from "cheapest buyable"
	// apart, because its first variant is both. The seed's own catalogue can:
	// aurora-slate-11's cheapest is out of stock, so position order pinned the
	// DEARER one — the tile advertised NT$14,900 起 and the page it linked to
	// opened at NT$17,900. A listing quotes the cheapest buyable variant, so
	// this has to agree with it or the price is a different number one click on.
	skewed := []Variant{
		{ID: "1", SKU: "S-1-1", PriceCents: 1490000, Sellable: false,
			Options: map[string]string{"顏色": "銀", "容量": "128GB"}},
		{ID: "2", SKU: "S-1-2", PriceCents: 1790000, Sellable: true, Available: 3,
			Options: map[string]string{"顏色": "銀", "容量": "256GB"}},
		{ID: "3", SKU: "S-2-1", PriceCents: 1490000, Sellable: true, Available: 4,
			Options: map[string]string{"顏色": "灰", "容量": "128GB"}},
	}
	if got, _ := Resolve(skewed, Selection{}); got.SKU != "S-2-1" {
		t.Errorf("Resolve(nothing chosen) SKU = %q, want S-2-1 — the cheapest buyable, "+
			"which is what the listing tile quoted", got.SKU)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, exact := Resolve(matrix(), tt.sel)
			if got.SKU != tt.wantSKU {
				t.Errorf("Resolve(%v) SKU = %q, want %q", tt.sel, got.SKU, tt.wantSKU)
			}
			if exact != tt.wantExact {
				t.Errorf("Resolve(%v) exact = %v, want %v", tt.sel, exact, tt.wantExact)
			}
		})
	}
}

func TestResolveRejectsUnknownValues(t *testing.T) {
	for _, sel := range []Selection{
		{"顏色": "螢光粉"},
		{"顏色": "星霧藍", "容量": "1TB"},
		{"材質": "鈦金屬"},
	} {
		if got, exact := Resolve(matrix(), sel); got.SKU != "" || exact {
			t.Errorf("Resolve(%v) = %q/%v, want no match", sel, got.SKU, exact)
		}
	}
}

func TestBuildOptionsMarksAvailabilityAgainstOtherChoices(t *testing.T) {
	groups := choices(map[string][]string{
		"顏色": {"星霧藍", "曜石黑"},
		"容量": {"256GB", "512GB"},
	})
	opts := BuildOptions("phone", groups, []string{"顏色", "容量"},
		map[string]string{"顏色": "Colour"}, matrix(), Selection{"容量": "512GB"})

	var colour Option
	for _, o := range opts {
		if o.Name == "顏色" {
			colour = o
		}
	}
	if len(colour.Values) != 2 {
		t.Fatalf("colour picker has %d values, want 2", len(colour.Values))
	}
	for _, v := range colour.Values {
		switch v.Value {
		case "星霧藍":
			if v.Available {
				t.Error("星霧藍 is marked available with 512GB chosen, but 星霧藍/512GB is sold out")
			}
		case "曜石黑":
			if !v.Available {
				t.Error("曜石黑 is marked unavailable with 512GB chosen, but 曜石黑/512GB can be bought")
			}
		}
	}
}

func TestBuildOptionsHrefKeepsOtherChoices(t *testing.T) {
	groups := choices(map[string][]string{"顏色": {"曜石黑"}, "容量": {"256GB", "512GB"}})
	opts := BuildOptions("phone", groups, []string{"顏色", "容量"},
		map[string]string{"顏色": "Colour"}, matrix(), Selection{"容量": "512GB"})

	for _, o := range opts {
		if o.Name != "顏色" {
			continue
		}
		got := o.Values[0].Href
		want := "/p/phone?" + url.Values{"容量": {"512GB"}, "顏色": {"曜石黑"}}.Encode()
		if got != want {
			t.Errorf("colour link = %q, want %q — the capacity choice was dropped", got, want)
		}
	}
}

func TestParseSelection(t *testing.T) {
	tests := []struct {
		name string
		in   url.Values
		want Selection
	}{
		{
			name: "reads option choices",
			in:   url.Values{"顏色": {"星霧藍"}, "容量": {"512GB"}},
			want: Selection{"顏色": "星霧藍", "容量": "512GB"},
		},
		{
			name: "ignores the page's own parameters",
			in:   url.Values{"顏色": {"星霧藍"}, "page": {"2"}, "q": {"x"}, "added": {"1"}},
			want: Selection{"顏色": "星霧藍"},
		},
		{
			name: "drops empty values",
			in:   url.Values{"顏色": {""}, "容量": {"  "}},
			want: Selection{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, ParseSelection(tt.in)); diff != "" {
				t.Errorf("ParseSelection() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseSelectionBoundsInput(t *testing.T) {
	long := make([]rune, maxOptionRunes+50)
	for i := range long {
		long[i] = '色'
	}
	got := ParseSelection(url.Values{"顏色": {string(long)}, string(long): {"x"}})

	if n := len([]rune(got["顏色"])); n != maxOptionRunes {
		t.Errorf("value kept %d runes, want it truncated to %d", n, maxOptionRunes)
	}
	if _, ok := got[string(long)]; ok {
		t.Error("an over-long option name was kept as a key")
	}
}

func choices(in map[string][]string) map[string][]OptionChoice {
	out := make(map[string][]OptionChoice, len(in))
	for name, values := range in {
		for _, v := range values {
			out[name] = append(out[name], OptionChoice{Value: v})
		}
	}
	return out
}

func TestThePickerShowsLabelsAndSelectsOnIdentity(t *testing.T) {
	t.Parallel()

	groups := map[string][]OptionChoice{
		"顏色": {{Value: "星霧藍", Label: "Mist Blue"}, {Value: "曜石黑", Label: "Obsidian"}},
		"容量": {{Value: "256GB"}, {Value: "512GB"}},
	}
	opts := BuildOptions("phone", groups, []string{"顏色", "容量"},
		map[string]string{"顏色": "Colour"}, matrix(), Selection{})

	var colour, capacity Option
	for _, o := range opts {
		switch o.Name {
		case "顏色":
			colour = o
		case "容量":
			capacity = o
		}
	}

	if colour.Label != "Colour" {
		t.Errorf("the picker heading reads %q, want Colour", colour.Label)
	}
	if colour.Name != "顏色" {
		t.Errorf("the option's identity is %q, want 顏色", colour.Name)
	}
	if colour.Values[0].Label != "Mist Blue" {
		t.Errorf("the swatch reads %q, want Mist Blue", colour.Values[0].Label)
	}
	if colour.Values[0].Value != "星霧藍" {
		t.Errorf("the swatch's identity is %q, want 星霧藍", colour.Values[0].Value)
	}
	if got := colour.Values[0].Href; !strings.Contains(got, url.QueryEscape("星霧藍")) {
		t.Errorf("href is %q, want the canonical 星霧藍 in it", got)
	}
	if strings.Contains(colour.Values[0].Href, "Mist") {
		t.Errorf("href is %q — it carries the LABEL, so a link shared with a "+
			"Chinese reader selects nothing", colour.Values[0].Href)
	}
	if capacity.Label != "容量" {
		t.Errorf("an untranslated option reads %q, want its Chinese name", capacity.Label)
	}
	if capacity.Values[0].Label != "256GB" {
		t.Errorf("an untranslated value reads %q, want 256GB", capacity.Values[0].Label)
	}
}

// TestThePagesOwnParametersAreNotVariantOptions holds a denylist that the page
// outran.
//
// ParseSelection treats every query key it does not RESERVE as a variant option,
// and reservedParam listed four: page, sort, q, added. The same handler reads
// three more off the same query — ?ask= and ?notify= are its own redirect
// outcomes, and ?p= is the comparison set /compare links back with. Each became
// a selection no variant could satisfy, so Resolve matched nothing and an
// in-stock product answered 找不到這個組合, lost its price box entirely, and
// emitted OutOfStock with a price of 0.00 in its JSON-LD.
//
// The restock form's own 303 lands on ?&notify=1, so asking to be told about a
// restock took the customer to a page saying the thing does not exist — and
// offered no confirmation, because the form is inside the block that vanished.
//
// Derived from the variants rather than listed: the next parameter somebody
// adds will not be added to a list either.
func TestThePagesOwnParametersAreNotVariantOptions(t *testing.T) {
	variants := matrix()

	tests := []struct {
		name string
		sel  Selection
		want int // how many keys survive as options
	}{
		{name: "the page's own outcome", sel: Selection{"ask": "1"}, want: 0},
		{name: "the restock redirect", sel: Selection{"notify": "1"}, want: 0},
		{name: "a comparison set", sel: Selection{"p": "pixelight-9"}, want: 0},
		{name: "a real option", sel: Selection{"顏色": "星霧藍"}, want: 1},
		{
			name: "a real option beside a page parameter",
			sel:  Selection{"顏色": "星霧藍", "notify": "1"},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(tt.sel.OnlyOptionsOf(variants)); got != tt.want {
				t.Errorf("OnlyOptionsOf(%v) kept %d keys, want %d", tt.sel, got, tt.want)
			}
		})
	}

	// And the consequence, not just the filter: a page parameter must leave the
	// product resolvable. Unfiltered, Resolve finds nothing at all.
	polluted := Selection{"notify": "1"}
	if _, exact := Resolve(variants, polluted.OnlyOptionsOf(variants)); exact {
		t.Error("a bare page parameter resolved an EXACT variant; it should leave the " +
			"choice open, not pin one")
	}
	if chosen, _ := Resolve(variants, polluted.OnlyOptionsOf(variants)); chosen.SKU == "" {
		t.Error("a page parameter left the product with no resolvable variant, so it " +
			"renders as sold out with no price")
	}

	// A wrong VALUE for a real option must still refuse: filtering keys must not
	// become filtering answers.
	if chosen, _ := Resolve(variants, Selection{"顏色": "沒有這個顏色"}.OnlyOptionsOf(variants)); chosen.SKU != "" {
		t.Error("a colour no variant has resolved to a variant")
	}
}
