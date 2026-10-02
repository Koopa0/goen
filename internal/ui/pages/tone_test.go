package pages_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/ui/pages"
)

// The schema's CHECK and app.css's [data-tone] blocks name the same six. The
// literal is repeated in assets/contrast_test.go, which cannot import pages.
func TestTonesAreTheClosedSet(t *testing.T) {
	t.Parallel()

	want := []pages.Tone{"paper", "stone", "mist", "sage", "blush", "ink"}
	if diff := cmp.Diff(want, pages.Tones()); diff != "" {
		t.Fatalf("Tones() mismatch (-want +got):\n%s", diff)
	}
	for _, tone := range want {
		if got, ok := pages.ParseTone(string(tone)); !ok || got != tone {
			t.Errorf("ParseTone(%q) = %q, %v", tone, got, ok)
		}
	}
	if _, ok := pages.ParseTone("neon"); ok {
		t.Error("ParseTone accepted a tone outside the set")
	}
}

func TestAnUnsetOrUnknownToneIsStone(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "neon", "STONE"} {
		if got := pages.ResolveTone(in); got != pages.ToneStone {
			t.Errorf("ResolveTone(%q) = %q, want stone", in, got)
		}
	}
	if got := pages.ResolveTone("sage"); got != pages.ToneSage {
		t.Errorf("ResolveTone(sage) = %q", got)
	}
}
