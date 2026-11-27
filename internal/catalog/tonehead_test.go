package catalog

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/tone"
)

// TestListingHeadTone holds that a department head wears no tone.
//
// The head is words on the page, as a catalogue's section opens; a tone ground
// behind it is the old panel the redesign took away. The campaign head keeps
// its tone: that is campaign's TestPageHeadTone.
func TestListingHeadTone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	listing := renderComponent(t, ctx, Listing(frame.Page{Title: "c"}, &ListingView{Slug: "c", Name: "Books", Theme: &Theme{Tone: tone.Sage}}))
	_, head, _ := strings.Cut(listing, `class="goen-shelf__head"`)
	head, _, _ = strings.Cut(head, `class="goen-facets"`)
	if head == "" {
		t.Fatalf("the listing has no head:\n%s", listing)
	}
	if strings.Contains(head, "data-tone") {
		t.Errorf("the category head wears a tone:\n%s", head)
	}
}
