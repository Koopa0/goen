package components_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/ui/components"
)

// The schema's CHECK and app.css's [data-tone] blocks name the same six. The
// literal is repeated in assets/contrast_test.go, which cannot import components.
func TestTonesAreTheClosedSet(t *testing.T) {
	t.Parallel()

	want := []components.Tone{"paper", "stone", "mist", "sage", "blush", "ink"}
	if diff := cmp.Diff(want, components.Tones()); diff != "" {
		t.Fatalf("Tones() mismatch (-want +got):\n%s", diff)
	}
	for _, tone := range want {
		if got, ok := components.ParseTone(string(tone)); !ok || got != tone {
			t.Errorf("ParseTone(%q) = %q, %v", tone, got, ok)
		}
	}
	if _, ok := components.ParseTone("neon"); ok {
		t.Error("ParseTone accepted a tone outside the set")
	}
}

func TestAnUnsetOrUnknownToneIsStone(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "neon", "STONE"} {
		if got := components.ResolveTone(in); got != components.ToneStone {
			t.Errorf("ResolveTone(%q) = %q, want stone", in, got)
		}
	}
	if got := components.ResolveTone("sage"); got != components.ToneSage {
		t.Errorf("ResolveTone(sage) = %q", got)
	}
}
