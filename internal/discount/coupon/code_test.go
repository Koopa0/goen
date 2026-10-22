package coupon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The back office stores a code as Normalise leaves it and checkout looks one
// up the same way, so these are the stored values.
func TestNormaliseAndValidCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typed string
		want  string
		valid bool
	}{
		{"summer-10", "SUMMER-10", true},
		{"  Spring \t\n", "SPRING", true},
		{"　AB ", "AB", true}, // ideographic and no-break spaces are trimmed
		{"00", "00", true},
		{strings.Repeat("a", 32), strings.Repeat("A", 32), true},
		{strings.Repeat("a", 33), strings.Repeat("A", 33), false},
		{"a", "A", false},
		{"", "", false},
		{"-AB", "-AB", false},
		{"AB C", "AB C", false},
		{"A_B", "A_B", false},
		// A Chinese keyboard left in full-width mode types the code itself.
		{"ＰＡＩＲ－ＡＭＴ", "PAIR-AMT", true},
		{"ｐａｉｒ－ａｍｔ", "PAIR-AMT", true},
		{"ＰＡＩＲ-amt", "PAIR-AMT", true},
		{"\u3000ＰＡＩＲ－ＡＭＴ\u3000", "PAIR-AMT", true},
		{"ＰＡＩＲ\u3000ＡＭＴ", "PAIR AMT", false},
		// Look-alikes stay what they are: the fold is not NFKC.
		{"РАІR-АМТ", "РАІR-АМТ", false},           // Cyrillic Р, А, І, А, М, Т
		{"PAIR\u2010AMT", "PAIR\u2010AMT", false}, // hyphen, not hyphen-minus
		{"ⅠⅠ", "ⅠⅠ", false},                       // Roman numeral one, which NFKC reads as I
		{"straße", "STRAßE", false},
		{"折扣10", "折扣10", false},
	}
	for _, tt := range tests {
		got := Normalise(tt.typed)
		if got != tt.want {
			t.Errorf("Normalise(%q) = %q, want %q", tt.typed, got, tt.want)
		}
		if ValidCode(got) != tt.valid {
			t.Errorf("ValidCode(%q) = %v, want %v", got, !tt.valid, tt.valid)
		}
	}
}

var codeFormat = regexp.MustCompile(`coupons_code_format CHECK \(code ~ '([^']*)'\)`)

func TestTheCodeShapeIsTheSchemas(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	m := codeFormat.FindSubmatch(schema)
	if m == nil {
		t.Fatal("coupons_code_format not found in the schema")
	}
	if got := codeShape.String(); got != string(m[1]) {
		t.Errorf("ValidCode matches %s; coupons_code_format admits %s", got, m[1])
	}
}
