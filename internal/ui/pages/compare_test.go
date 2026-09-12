package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestCompareShowsTheTableOnlyWhenThereAreTwoProducts holds Enough() and the
// markup it gates. One product is the too-few empty state; the table class
// exists only when there are two columns. A layout row that marks the wrapper
// would pass either way.
func TestCompareShowsTheTableOnlyWhenThereAreTwoProducts(t *testing.T) {
	t.Parallel()

	page := layouts.Page{Title: "比較"}

	if (CompareView{Products: []CompareProduct{{Slug: "a"}}}).Enough() {
		t.Fatal("one product is enough — the empty state would never render")
	}
	if !(CompareView{Products: []CompareProduct{{Slug: "a"}, {Slug: "b"}}}).Enough() {
		t.Fatal("two products are not enough — the table would never render")
	}

	one := renderToString(t, Compare(page, CompareView{
		Products: []CompareProduct{{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro"}},
	}))
	if strings.Contains(one, `class="goen-compare__table"`) {
		t.Error("one product rendered the comparison table")
	}
	if !strings.Contains(one, `class="ui-empty"`) {
		t.Error("one product did not render the too-few empty state")
	}

	two := renderToString(t, Compare(page, CompareView{
		Products: []CompareProduct{
			{Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro"},
			{Slug: "pixelight-9", Name: "Pixelight 9"},
		},
	}))
	if !strings.Contains(two, `class="goen-compare__table"`) {
		t.Error("two products did not render the comparison table")
	}
	if strings.Contains(two, `class="ui-empty"`) {
		t.Error("two products still rendered the too-few empty state")
	}
}
