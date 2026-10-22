package returns

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// unsentReasonCheck is the list return_requests_unsent_reason_known admits.
var unsentReasonCheck = regexp.MustCompile(
	`(?s)CONSTRAINT return_requests_unsent_reason_known CHECK \(.*?reason IN \(([^)]*)\)`)

var quoted = regexp.MustCompile(`'([a-z_]+)'`)

// TestUnsentReasonsMatchSchema checks that UnsentReasons are the codes the
// schema admits, that ParseUnsentReason reads each back, and that it refuses a
// typo.
func TestUnsentReasonsMatchSchema(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	check := unsentReasonCheck.FindSubmatch(schema)
	if check == nil {
		t.Fatal("return_requests_unsent_reason_known not found; the pattern no longer matches the schema")
	}
	codes := quoted.FindAllSubmatch(check[1], -1)
	want := make([]string, 0, len(codes))
	for _, m := range codes {
		want = append(want, string(m[1]))
	}
	got := make([]string, 0, len(UnsentReasons))
	for _, r := range UnsentReasons {
		got = append(got, string(r))
		if parsed, ok := ParseUnsentReason(string(r)); !ok || parsed != r {
			t.Errorf("ParseUnsentReason(%q) = %q, %t", r, parsed, ok)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("UnsentReasons = %v, the schema admits %v", got, want)
	}
	for _, typo := range []string{"", "Damaged", "broken", "customer_withdrew "} {
		if _, ok := ParseUnsentReason(typo); ok {
			t.Errorf("ParseUnsentReason(%q) accepted a code the schema refuses", typo)
		}
	}
}
