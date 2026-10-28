package frame_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/pagestatus"
)

var documentTitle = regexp.MustCompile(`<title>([^<]*)</title>`)

// TestAdminTitleSaysNotSavedOn422: a back-office page answered with 422 opens
// its title with the refusal, a page answered with 200 does not, and a page that
// reports a refusal beside work that went through keeps its own title.
func TestAdminTitleSaysNotSavedOn422(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		status int
		done   bool
		shop   bool
		want   string
	}{
		{i18n.En, http.StatusUnprocessableEntity, false, false, "Not saved: Mug · goen"},
		{i18n.ZhHant, http.StatusUnprocessableEntity, false, false, "未儲存：Mug · goen"},
		{i18n.En, http.StatusOK, false, false, "Mug · goen"},
		{i18n.En, http.StatusNotFound, false, false, "Mug · goen"},
		{i18n.En, http.StatusUnprocessableEntity, true, false, "Mug · goen"},
		{i18n.En, http.StatusUnprocessableEntity, false, true, "Mug · goen"},
	} {
		t.Run(tt.locale.Tag()+"/"+http.StatusText(tt.status)+map[bool]string{true: "/done", false: ""}[tt.done]+map[bool]string{true: "/shop", false: ""}[tt.shop], func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			ctx = pagestatus.With(ctx, tt.status)
			page := frame.Page{Title: "Mug", PartlyDone: tt.done}
			component := frame.Admin(page, "products")
			if tt.shop {
				component = frame.Base(page)
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
