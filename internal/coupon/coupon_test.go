package coupon

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want Kind
		ok   bool
	}{
		{"amount", Amount, true},
		{"percent", Percent, true},
		{"free_shipping", FreeShipping, true},
		{"", "", false},
		{"Percent", "", false},
		{"bogo", "", false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Parse(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

var kindsAdmitted = regexp.MustCompile(`coupons_kind_known CHECK \(kind IN \(([^)]*)\)\)`)

func TestEveryAdmittedKindHasAConstant(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	m := kindsAdmitted.FindSubmatch(schema)
	if m == nil {
		t.Fatal("coupons_kind_known not found in the schema")
	}
	for _, lit := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(m[1], -1) {
		if _, ok := Parse(string(lit[1])); !ok {
			t.Errorf("the schema admits kind %q and Parse refuses it", lit[1])
		}
	}
}
