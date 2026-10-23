package inventory

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// reasonCheck is the list inventory_movements_reason_known admits.
var reasonCheck = regexp.MustCompile(
	`(?s)CONSTRAINT inventory_movements_reason_known CHECK \(reason IN \(([^)]*)\)`)

var quoted = regexp.MustCompile(`'([a-z_]+)'`)

// TestMovementReason_MatchesSchema checks that the reasons are the set the
// schema admits and that each has its own label in both languages, so a reason
// added to one side without the other fails here and not as a 500 on the stock
// page.
func TestMovementReason_MatchesSchema(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	check := reasonCheck.FindSubmatch(schema)
	if check == nil {
		t.Fatal("inventory_movements_reason_known not found; the pattern no longer matches the schema")
	}
	codes := quoted.FindAllSubmatch(check[1], -1)
	want := make([]string, 0, len(codes))
	for _, m := range codes {
		want = append(want, string(m[1]))
	}
	got := make([]string, 0, len(reasons))
	for _, r := range reasons {
		got = append(got, string(r.reason))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("reasons = %v, the schema admits %v", got, want)
	}

	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		seen := map[string]MovementReason{}
		for _, known := range reasons {
			r := known.reason
			label := r.Label(ctx)
			if label == "" || label == string(r) {
				t.Errorf("%s: %q is labelled %q, which explains nothing", locale.Tag(), r, label)
			}
			if other, dup := seen[label]; dup {
				t.Errorf("%s: %q and %q are both labelled %q", locale.Tag(), other, r, label)
			}
			seen[label] = r
		}
	}
}

// TestMovementReason_LabelUnknownPanics holds that a reason outside the set is
// refused loudly rather than printed as a raw code.
func TestMovementReason_LabelUnknownPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		got, _ := recover().(string)
		if !strings.HasPrefix(got, "inventory: no label for movement reason ") {
			t.Errorf("Label(%q) panicked with %q", "damaged", got)
		}
	}()
	MovementReason("damaged").Label(t.Context())
	t.Error("Label accepted a reason outside the set")
}
