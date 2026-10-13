package layouts_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAdminSkipLinkBypassesTheSidebar(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name    string
			current string
			target  string
		}{
			{name: "orders", current: "orders", target: `<div id="main" class="goen-admin__main" tabindex="-1">`},
			{name: "step-up", target: `<main id="main" tabindex="-1">`},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				const content = `<button type="button">work</button>`
				ctx := templ.WithChildren(i18n.WithLocale(t.Context(), locale), templ.Raw(content))
				var b strings.Builder
				if err := layouts.Admin(layouts.Page{Title: "t"}, tt.current).Render(ctx, &b); err != nil {
					t.Fatalf("render Admin: %v", err)
				}
				body := b.String()
				const skip = `<a class="goen-skip" href="#main">`
				if first := strings.Index(body, "<a "); first < 0 || !strings.HasPrefix(body[first:], skip) {
					t.Error("the first link is not the skip link")
				}
				if got := strings.Count(body, `id="main"`); got != 1 {
					t.Errorf("skip target count = %d, want 1", got)
				}
				targetAt := strings.Index(body, tt.target)
				if targetAt < 0 {
					t.Fatalf("Admin skip target is missing or not focusable: want %s", tt.target)
				}
				if !strings.HasPrefix(body[targetAt+len(tt.target):], content) {
					t.Error("the skip target does not start with the page content")
				}
				navEnd := strings.Index(body, "</nav>")
				if tt.current == "" {
					if navEnd >= 0 {
						t.Error("the step-up page has sidebar navigation")
					}
				} else if navEnd < 0 || navEnd >= targetAt {
					t.Error("the skip target does not bypass the sidebar navigation")
				}
			})
		}
	}
}
