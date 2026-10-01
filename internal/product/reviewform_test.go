package product

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

// The form's minlength is what lets a short review be warned about in the
// browser rather than refused by the server after the round trip.
func TestTheReviewFormWarnsAtTheSameLengthTheServerRefuses(t *testing.T) {
	t.Parallel()
	if pages.ReviewBodyMinRunes != MinReviewBodyRunes {
		t.Errorf("form minlength %d, server minimum %d", pages.ReviewBodyMinRunes, MinReviewBodyRunes)
	}
}
