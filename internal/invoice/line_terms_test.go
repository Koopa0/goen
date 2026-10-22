package invoice

import (
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestProductTaxTypesZeroRated checks that a product's tax types exclude
// zero-rated: ProductTaxTypes lists only taxable and exempt, and
// LineTerms.Validate refuses the rest, though TaxType.Known still admits
// zero-rated.
func TestProductTaxTypesZeroRated(t *testing.T) {
	t.Parallel()
	if !slices.Equal(ProductTaxTypes(), []TaxType{"taxable", "exempt"}) {
		t.Fatalf("product tax types=%v", ProductTaxTypes())
	}
	for _, tax := range []TaxType{"taxable", "exempt", "zero_rated"} {
		if !tax.Known() {
			t.Errorf("known tax type %q refused", tax)
		}
	}
	if TaxType("mixed").Known() || TaxType("").Known() {
		t.Error("unknown tax type accepted")
	}
	for _, tax := range []TaxType{Taxable, Exempt} {
		if errs := (LineTerms{TaxType: tax, Unit: DefaultUnit}).Validate(t.Context()); len(errs) != 0 {
			t.Errorf("tax %q: %v", tax, errs)
		}
	}
	for _, tax := range []TaxType{ZeroRated, "", "mixed"} {
		if errs := (LineTerms{TaxType: tax, Unit: DefaultUnit}).Validate(t.Context()); errs["tax_type"] == "" {
			t.Errorf("product accepted tax %q", tax)
		}
	}
}

// TestItemUnitBoundsAndTaxLabels checks that an invoice unit is at most six
// characters with no control character, and that each tax type has a label in
// both locales.
func TestItemUnitBoundsAndTaxLabels(t *testing.T) {
	t.Parallel()
	for _, unit := range append(CommonItemUnits(), ItemUnit(strings.Repeat("箱", 6)), ItemUnit("自訂")) {
		if !unit.Valid() {
			t.Errorf("valid unit %q refused", unit)
		}
	}
	for _, unit := range []ItemUnit{"", " ", "個\n", ItemUnit(strings.Repeat("箱", 7)), ItemUnit(string([]byte{0xff}))} {
		facts := LineTerms{TaxType: Taxable, Unit: unit}
		if facts.Validate(t.Context())["invoice_unit"] == "" {
			t.Errorf("invalid unit %q accepted", unit)
		}
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if Taxable.Label(ctx) == "" || Exempt.Label(ctx) == "" || ZeroRated.Label(ctx) == "" {
			t.Errorf("%s tax labels missing", locale)
		}
	}
}
