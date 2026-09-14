package layoutcheck_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestWishlistLayoutRowsMarkTheComposedGrid holds the wishlist fixture row.
// A descendant tile selector or a global remove form would pass the #320
// topology where the card and action are separate nodes.
func TestWishlistLayoutRowsMarkTheComposedGrid(t *testing.T) {
	t.Parallel()

	body := readLayoutScript(t, repoRoot(t))

	for _, label := range []string{"wishlist 375", "wishlist 1440"} {
		row := wishlistLayoutRow(t, body, label)
		if !strings.Contains(row, "wishlist: true") {
			t.Errorf("%s is not a wishlist row:\n%s", label, row)
		}
		if strings.Contains(row, ".goen-tiles__grid .goen-tile") {
			t.Errorf("%s still marks any descendant tile instead of a composed row:\n%s", label, row)
		}
		if !strings.Contains(row, `marker: '.goen-tiles__grid > li.goen-wish'`) {
			t.Errorf("%s does not mark a direct grid list item:\n%s", label, row)
		}
	}
}

// TestWishlistLayoutProbeRequiresSameItemComposition holds the measurements
// that only exist when each saved product is one grid list item: direct
// children, no nested list items, and matching card/remove slugs per row.
func TestWishlistLayoutProbeRequiresSameItemComposition(t *testing.T) {
	t.Parallel()

	body := readLayoutScript(t, repoRoot(t))
	for _, needle := range []string{
		"grid.children",
		"child.tagName !== 'LI'",
		"child.querySelector('li')",
		"slugFromCard",
		"slugFromForm",
		"cardSlug !== formSlug",
		"got.composed < 1",
		"got.items !== got.composed",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("the wishlist probe never reads %q", needle)
		}
	}
}

func wishlistLayoutRow(t *testing.T, body, label string) string {
	t.Helper()
	re := regexp.MustCompile(`\{ label: '` + regexp.QuoteMeta(label) + `',[^}]+\}`)
	row := re.FindString(body)
	if row == "" {
		t.Fatalf("no layout row labelled %q", label)
	}
	return row
}
