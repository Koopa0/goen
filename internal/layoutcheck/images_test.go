package layoutcheck_test

import (
	"strings"
	"testing"
)

// TestTheLayoutGateWaitsForImagesBeforeMeasuringThem refuses a probe that reads
// naturalWidth off a fetch which has not settled.
//
// readyState complete does not wait for images, so on a cold cache the first
// page of a run is measured mid-download and a slow byte is reported as an
// absent one. CI did exactly that: two product images failed at the 375
// artboard, the run's first navigation, and loaded correctly at 768, 1024 and
// 1440 seconds later.
func TestTheLayoutGateWaitsForImagesBeforeMeasuringThem(t *testing.T) {
	t.Parallel()

	body := readLayoutScript(t, repoRoot(t))

	if !strings.Contains(body, "const imagesFetched = async (label) => {") {
		t.Fatal("check-layout.mjs no longer waits for images to finish fetching")
	}

	// The wait keys on the FETCH having settled and nothing else. naturalWidth
	// cannot tell a slow image from an absent one; `complete` is true for both
	// outcomes, which is what keeps a 404 failing immediately.
	if !strings.Contains(body, "filter((img) => !img.complete)") {
		t.Fatal("the image wait must key on `complete`, or it waits for a missing image too")
	}

	// A wait after the measurement measures the same thing it did before.
	wait := strings.Index(body, "await imagesFetched(want.label);")
	probe := strings.Index(body, "const evaluated = await send(ws, 'Runtime.evaluate', { expression: PROBE")
	if wait < 0 {
		t.Fatal("the home-page rows do not wait for their images")
	}
	if probe < 0 || wait > probe {
		t.Fatal("check-layout.mjs must wait for the images before it evaluates PROBE")
	}
}
