package admin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// The move forms post the image's own key and one of the three words the store
// accepts; the first image cannot go earlier, the last cannot go later.
func TestImageListMoveFormsPostTheRightImageAndWord(t *testing.T) {
	t.Parallel()
	keys := []string{strings.Repeat("a1", 32), strings.Repeat("b2", 32), strings.Repeat("c3", 32)}
	v := ProductView{Slug: "p", Images: []Image{{Key: keys[0], Alt: "x"}, {Key: keys[1], Alt: "y"}, {Key: keys[2], Alt: "z"}}}
	var b strings.Builder
	if err := productImages(v).Render(i18n.WithLocale(t.Context(), i18n.En), &b); err != nil {
		t.Fatal(err)
	}
	forms := map[string]int{}
	re := regexp.MustCompile(`(?s)<form method="post" action="/admin/products/p/images/move">\s*<input type="hidden" name="digest" value="([0-9a-f]+)"/?>\s*<input type="hidden" name="move" value="([a-z]+)"/?>`)
	for _, m := range re.FindAllStringSubmatch(b.String(), -1) {
		forms[m[1]+" "+m[2]]++
	}
	want := map[string]int{
		keys[1] + " cover": 1, keys[1] + " up": 1, keys[1] + " down": 1,
		keys[2] + " cover": 1, keys[2] + " up": 1,
		keys[0] + " down": 1,
	}
	if len(forms) != len(want) {
		t.Fatalf("move forms = %v, want %v", forms, want)
	}
	for k, n := range want {
		if forms[k] != n {
			t.Errorf("move form %q appears %d times, want %d (all: %v)", k, forms[k], n, forms)
		}
	}
}
