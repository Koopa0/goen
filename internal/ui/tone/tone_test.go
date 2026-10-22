package tone_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/ui/tone"
)

// The schema's CHECK and app.css's [data-tone] blocks name the same six;
// assets/contrast_test.go holds the sheet to this set.
func TestAllIsTheClosedSet(t *testing.T) {
	t.Parallel()

	want := []tone.Tone{"paper", "stone", "mist", "sage", "blush", "ink"}
	if diff := cmp.Diff(want, slices.Collect(tone.All())); diff != "" {
		t.Fatalf("All() mismatch (-want +got):\n%s", diff)
	}
	for _, v := range want {
		if got, ok := tone.Parse(string(v)); !ok || got != v {
			t.Errorf("Parse(%q) = %q, %v", v, got, ok)
		}
	}
	if _, ok := tone.Parse("neon"); ok {
		t.Error("Parse accepted a tone outside the set")
	}
}

func TestAnUnsetOrUnknownToneIsStone(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "neon", "STONE"} {
		if got := tone.Resolve(in); got != tone.Stone {
			t.Errorf("Resolve(%q) = %q, want stone", in, got)
		}
	}
	if got := tone.Resolve("sage"); got != tone.Sage {
		t.Errorf("Resolve(sage) = %q", got)
	}
}
