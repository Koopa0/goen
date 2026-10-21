package cart

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// From internal/ui/pages/brandimagery_test.go: the cart's row of
// TestEmptyStatesShowTheirIllustrationAsDecoration.
func TestCartEmptyStateShowsItsIllustration(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		page  string
		asset string
	}{
		"cart": {renderIn(t, i18n.ZhHant, Page(layouts.Page{}, View{})), assets.EmptyCartImage},
	} {
		want := `<img class="goen-empty__art" src="` + assets.URL(tt.asset) + `" alt="" width="128" height="96"`
		if !strings.Contains(tt.page, want) {
			t.Errorf("%s empty state omits %s", name, want)
		}
	}
}
