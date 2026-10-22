package catalog

import (
	"testing"
)

// TestReviewBodyRunes holds that the review form warns at the same length the
// server refuses.
//
// The form's minlength is what lets a short review be warned about in the
// browser rather than refused by the server after the round trip.
func TestReviewBodyRunes(t *testing.T) {
	t.Parallel()
	if ReviewBodyMinRunes != MinReviewBodyRunes {
		t.Errorf("form minlength %d, server minimum %d", ReviewBodyMinRunes, MinReviewBodyRunes)
	}
	if ReviewBodyMaxRunes != MaxReviewBodyRunes {
		t.Errorf("form maxlength %d, server maximum %d", ReviewBodyMaxRunes, MaxReviewBodyRunes)
	}
}
