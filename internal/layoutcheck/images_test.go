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

// TestTheLayoutGateScrollsLazyImagesIntoViewBeforeWaiting refuses a wait that
// polls an image the browser was never asked to fetch.
//
// The home page's product tiles carry loading="lazy" below the fold, which is
// what a shopper on a phone should be served. A driven viewport never scrolls,
// so at the narrow artboard the deepest row stays outside the distance Chrome
// starts a lazy fetch at: those images sit at complete=false for the whole
// ceiling and are then reported as images the server does not serve. CI said so
// twice, naming the two tiles in the last row, which its own server log shows
// were never requested until the viewport widened for the next artboard.
func TestTheLayoutGateScrollsLazyImagesIntoViewBeforeWaiting(t *testing.T) {
	t.Parallel()

	body := readLayoutScript(t, repoRoot(t))

	wait := strings.Index(body, "const imagesFetched = async (label) => {")
	if wait < 0 {
		t.Fatal("check-layout.mjs no longer waits for images to finish fetching")
	}
	scroll := strings.Index(body[wait:], "window.scrollTo(0, y);")
	poll := strings.Index(body[wait:], "filter((img) => !img.complete)")
	if scroll < 0 {
		t.Fatal("the image wait must scroll the page through, or a lazy tile is never fetched")
	}
	if poll < 0 || scroll > poll {
		t.Fatal("the scroll must come before the poll, or it waits on a fetch nobody asked for")
	}

	// Back to the top before anything is measured: every geometry assertion in
	// the file reads a box off the live layout at scroll 0.
	top := strings.Index(body[wait:], "window.scrollTo(0, 0);")
	if top < 0 || top < scroll || top > poll {
		t.Fatal("the image wait must return to the top of the document before it polls")
	}
}
