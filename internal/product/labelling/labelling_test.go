package labelling

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNetUnitSymbolForPiecesUsesTheLocaleCountUnit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{locale: i18n.En, want: "pcs"},
		{locale: i18n.ZhHant, want: "件"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			if got := Piece.Symbol(i18n.WithLocale(t.Context(), tc.locale)); got != tc.want {
				t.Errorf("Piece.Symbol() = %q, want %q", got, tc.want)
			}
		})
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

// TestTextLimitsMatchTheSchemaChecks reads the char_length bound out of each
// products_label_*_valid CHECK in the initial migration, so the constants cannot
// drift from the schema.
func TestTextLimitsMatchTheSchemaChecks(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		check string
		want  int
	}{
		{"products_label_origin_valid", MaxOriginRunes},
		{"products_label_origin_en_valid", MaxOriginEnRunes},
		{"products_label_domestic_name_valid", MaxPartyNameRunes},
		{"products_label_domestic_phone_valid", MaxPartyPhoneRunes},
		{"products_label_domestic_address_valid", MaxPartyAddressRunes},
	} {
		re := regexp.MustCompile(`CONSTRAINT ` + tc.check + ` CHECK \(.*?char_length\(\w+\) <= (\d+)`)
		m := re.FindSubmatch(schema)
		if m == nil {
			t.Errorf("%s: no char_length bound found in the schema", tc.check)
			continue
		}
		if got, _ := strconv.Atoi(string(m[1])); got != tc.want {
			t.Errorf("%s bounds %d characters, the constant says %d", tc.check, got, tc.want)
		}
	}
}
