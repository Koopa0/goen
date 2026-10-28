package frame_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

var documentTitle = regexp.MustCompile(`<title>([^<]*)</title>`)

// TestAdminTitleSaysNotSavedWhenRefused: a back-office page that sets Refused
// opens its title with the refusal in the reader's language, one that does not
// keeps its own title, and the storefront frame does not read the field.
func TestAdminTitleSaysNotSavedWhenRefused(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		locale  i18n.Locale
		page    frame.Page
		storefr bool
		want    string
	}{
		{"refused en", i18n.En, frame.Page{Title: "Mug", Refused: true}, false, "Not saved: Mug · goen"},
		{"refused zh-Hant", i18n.ZhHant, frame.Page{Title: "Mug", Refused: true}, false, "未儲存：Mug · goen"},
		{"not refused en", i18n.En, frame.Page{Title: "Mug"}, false, "Mug · goen"},
		{"not refused zh-Hant", i18n.ZhHant, frame.Page{Title: "Mug"}, false, "Mug · goen"},
		{"refused on the storefront frame", i18n.En, frame.Page{Title: "Mug", Refused: true}, true, "Mug · goen"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			component := frame.Admin(tt.page, "products")
			if tt.storefr {
				component = frame.Base(tt.page)
			}
			var body strings.Builder
			if err := component.Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			m := documentTitle.FindStringSubmatch(body.String())
			if m == nil {
				t.Fatal("the page has no <title>")
			}
			if m[1] != tt.want {
				t.Errorf("<title> = %q, want %q", m[1], tt.want)
			}
		})
	}
}
