package issuer

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// buyerConsent is the buyer's consent to an e-invoice allowance, kept as a tax
// record: erase_user keeps it on purpose. Its keys are stored and the schema
// refuses a consent without them, so keys are added, never renamed.
func TestBuyerConsentWireForm(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(buyerConsent{AgreedAt: "2026-08-07 15:22:00", IP: "203.0.113.7", Email: "a@example.com"})
	if err != nil {
		t.Fatalf("marshal buyerConsent: %v", err)
	}
	const want = `{"agreed_at":"2026-08-07 15:22:00","ip":"203.0.113.7","email":"a@example.com"}`
	if string(got) != want {
		t.Errorf("buyerConsent marshals to %s, want %s", got, want)
	}
}

// The settlement function in the schema refuses a consent that lacks any of its
// keys, so the struct must write every one of them.
func TestBuyerConsentKeysSatisfyTheSchema(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	match := regexp.MustCompile(`p_buyer_consent\s*\?&\s*ARRAY\[([^\]]*)\]`).FindSubmatch(schema)
	if match == nil {
		t.Fatal("001_initial_schema.up.sql no longer checks p_buyer_consent ?& ARRAY[...]: " +
			"the schema moved, so update this test, do not skip it")
	}
	var required []string
	for _, q := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(match[1], -1) {
		required = append(required, string(q[1]))
	}

	typ := reflect.TypeFor[buyerConsent]()
	var written []string
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		written = append(written, name)
	}

	slices.Sort(required)
	slices.Sort(written)
	if !slices.Equal(required, written) {
		t.Errorf("buyerConsent writes keys %v, but the schema requires %v", written, required)
	}
}
