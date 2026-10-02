package admin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros holds that the banner form's
// errors are keyed apart from the hero form's: both sit on one page and share
// one map, and both have a days field.
func TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := HeroView{Errors: map[string]string{
		"banner_days": "檔期天數必須介於 0(不限)到 365 天。",
		"banner_cta":  "連結必須是本站路徑,例如 /deals。",
	}}
	html := renderComponent(t, ctx, Home(layouts.Page{}, &view))

	input := func(id string) string {
		m := regexp.MustCompile(`<input[^>]*\bid="` + id + `"[^>]*>`).FindString(html)
		if m == "" {
			t.Fatalf("no input %q on the page", id)
		}
		return m
	}
	if strings.Contains(input("h-days"), "aria-invalid") {
		t.Error("the hero's days is marked invalid by a refused banner")
	}
	for id, describedBy := range map[string]string{"b-days": "b-days-error", "b-cta-href": "b-cta-error"} {
		in := input(id)
		if !strings.Contains(in, `aria-invalid="true"`) || !strings.Contains(in, `aria-describedby="`+describedBy+`"`) {
			t.Errorf("%s is not marked invalid and linked to %s: %s", id, describedBy, in)
		}
		if !strings.Contains(html, `id="`+describedBy+`"`) {
			t.Errorf("no message element %q", describedBy)
		}
	}
	if strings.Contains(html, `id="h-days-error"`) {
		t.Error("the hero's days shows a message for the banner's refusal")
	}
}
