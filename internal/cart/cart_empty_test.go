package cart

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestPageEmptyHasNoPicture holds that an empty cart says so in words and
// offers one way on, without a picture. The other pages' rows of this test
// are catalog's TestListingEmptyStatesHaveNoPicture.
func TestPageEmptyHasNoPicture(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(frame.Page{}, View{}))
	_, rest, ok := strings.Cut(page, `class="goen-empty">`)
	if !ok {
		t.Fatal("empty cart renders no goen-empty box")
	}
	box, _, _ := strings.Cut(rest, `</div>`)
	for _, picture := range []string{`<img`, `goen-medallion`} {
		if strings.Contains(box, picture) {
			t.Errorf("empty cart draws %q", picture)
		}
	}
	if want := `<a class="goen-btn goen-btn--primary goen-btn--lg" href="/"`; !strings.Contains(box, want) {
		t.Errorf("empty cart omits %s", want)
	}
}
