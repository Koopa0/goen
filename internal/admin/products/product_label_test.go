package products

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/product/labelling"
)

// TestValidateLabelNetQuantity: a net quantity and its unit are optional
// together.
func TestValidateLabelNetQuantity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		quantity string
		unit     labelling.NetUnit
		valid    bool
	}{
		{"", "", true}, {"1", labelling.Gram, true}, {" 0.01 ", labelling.Millilitre, true},
		{"99999999.99", labelling.Piece, true}, {"1", "", false}, {"", labelling.Gram, false},
		{"0", labelling.Gram, false}, {"-1", labelling.Gram, false}, {"1e2", labelling.Gram, false},
		{"1.001", labelling.Gram, false}, {"100000000", labelling.Gram, false}, {"1", "oz", false},
	} {
		input := LabelInput{NetQuantity: tc.quantity, NetUnit: tc.unit}
		got := len(validateLabel(t.Context(), &input)) == 0
		if got != tc.valid {
			t.Errorf("quantity %q, unit %q: valid=%v, want %v", tc.quantity, tc.unit, got, tc.valid)
		}
	}
	for raw, want := range map[string]int64{"1": 100, "1.2": 120, "0.01": 1, "99999999.99": 9999999999} {
		if got, ok := quantityHundredths(raw); !ok || got != want {
			t.Errorf("%q: hundredths=%d, valid=%v, want %d", raw, got, ok, want)
		}
	}
}

// TestValidateLabelTextAndAge: label text and age are bounded.
func TestValidateLabelTextAndAge(t *testing.T) {
	t.Parallel()
	for _, field := range (&LabelInput{}).TextFields() {
		for _, raw := range []string{strings.Repeat("界", field.Limit+1), "a\n", string([]byte{0xff})} {
			input := LabelInput{}
			switch field.Name {
			case "origin":
				input.Origin = raw
			case "origin_en":
				input.OriginEn = raw
			case "domestic_party_name":
				input.DomesticPartyName = raw
			case "domestic_party_phone":
				input.DomesticPartyPhone = raw
			case "domestic_party_address":
				input.DomesticPartyAddress = raw
			}
			if validateLabel(t.Context(), &input)[field.Name] == "" {
				t.Errorf("%s accepted invalid text", field.Name)
			}
		}
	}
	for _, raw := range []string{"", "0", "216"} {
		if _, ok := ageMonths(raw); !ok {
			t.Errorf("rejected age %q", raw)
		}
	}
	for _, raw := range []string{"217", "-1", "1.5", "1e2", "32768"} {
		input := LabelInput{MinAgeMonths: raw}
		if validateLabel(t.Context(), &input)["min_age_months"] == "" {
			t.Errorf("accepted age %q", raw)
		}
	}
}

// TestValidateLabelTrimmed: label text limits count the stored, trimmed value.
func TestValidateLabelTrimmed(t *testing.T) {
	t.Parallel()
	input := LabelInput{
		Origin:               " " + strings.Repeat("界", 100) + " ",
		OriginEn:             " " + strings.Repeat("a", 100) + " ",
		DomesticPartyName:    " " + strings.Repeat("界", 200) + " ",
		DomesticPartyPhone:   " " + strings.Repeat("1", 40) + " ",
		DomesticPartyAddress: " " + strings.Repeat("界", 500) + " ",
	}
	if errs := validateLabel(t.Context(), &input); len(errs) != 0 {
		t.Errorf("legal trimmed label fields refused: %v", errs)
	}
}
