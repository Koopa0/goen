package productlabel

import (
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNetQuantityAndUnitAreOptionalTogether(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		quantity string
		unit     NetUnit
		valid    bool
	}{
		{"", "", true}, {"1", Gram, true}, {" 0.01 ", Millilitre, true},
		{"99999999.99", Piece, true}, {"1", "", false}, {"", Gram, false},
		{"0", Gram, false}, {"-1", Gram, false}, {"1e2", Gram, false},
		{"1.001", Gram, false}, {"100000000", Gram, false}, {"1", "oz", false},
	} {
		input := Input{NetQuantity: tc.quantity, NetUnit: tc.unit}
		got := len(input.Validate(t.Context())) == 0
		if got != tc.valid {
			t.Errorf("quantity %q, unit %q: valid=%v, want %v", tc.quantity, tc.unit, got, tc.valid)
		}
	}
	for raw, want := range map[string]int64{"1": 100, "1.2": 120, "0.01": 1, "99999999.99": 9999999999} {
		if got, ok := QuantityHundredths(raw); !ok || got != want {
			t.Errorf("%q: hundredths=%d, valid=%v, want %d", raw, got, ok, want)
		}
	}
}

func TestLabelTextAndAgeBounds(t *testing.T) {
	t.Parallel()
	for _, field := range (&Input{}).TextFields() {
		for _, raw := range []string{strings.Repeat("界", field.Limit+1), "a\n", string([]byte{0xff})} {
			input := Input{}
			switch field.Name {
			case "origin":
				input.Origin = raw
			case "origin_en":
				input.OriginEn = raw
			case "responsible_party_name":
				input.ResponsiblePartyName = raw
			case "responsible_party_phone":
				input.ResponsiblePartyPhone = raw
			case "responsible_party_address":
				input.ResponsiblePartyAddress = raw
			}
			if input.Validate(t.Context())[field.Name] == "" {
				t.Errorf("%s accepted invalid text", field.Name)
			}
		}
	}
	for _, raw := range []string{"", "0", "216"} {
		if _, ok := AgeMonths(raw); !ok {
			t.Errorf("rejected age %q", raw)
		}
	}
	for _, raw := range []string{"217", "-1", "1.5", "1e2", "32768"} {
		input := Input{MinAgeMonths: raw}
		if input.Validate(t.Context())["min_age_months"] == "" {
			t.Errorf("accepted age %q", raw)
		}
	}
}

func TestFactsKeepZeroAgeAndHideUnsetFields(t *testing.T) {
	t.Parallel()
	var unset *Facts
	if len(unset.Rows(t.Context())) != 0 || len((&Facts{}).Rows(t.Context())) != 0 {
		t.Fatal("unset facts rendered rows")
	}
	age := int16(0)
	facts := Facts{NetQuantity: "1.20", NetUnit: Piece, MinAgeMonths: &age}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		rows := facts.Rows(ctx)
		if len(rows) != 2 || rows[0].Value != "1.20 "+i18n.T(ctx, i18n.KeyProductLabelPiece) || !strings.Contains(rows[1].Value, "0") {
			t.Fatalf("%s: rows=%+v", locale, rows)
		}
	}
}

func TestNetUnitVocabulary(t *testing.T) {
	t.Parallel()
	want := []NetUnit{"g", "kg", "ml", "l", "piece"}
	if !slices.Equal(Units(), want) {
		t.Fatalf("net units=%v, want %v", Units(), want)
	}
	for _, unit := range want {
		if !unit.Known() {
			t.Errorf("known unit %q refused", unit)
		}
	}
	for _, unit := range []NetUnit{"", "oz", "G"} {
		if unit.Known() {
			t.Errorf("unknown unit %q accepted", unit)
		}
	}
}
