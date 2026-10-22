package widget_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/widget"
)

// A phone's stylesheet shows only the last link of the trail, so the trail
// ends in that link, a separator and the current page, which alone is marked.
func TestBreadcrumbMarksOnlyTheCurrentPage(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	err := widget.Breadcrumb(widget.BreadcrumbProps{
		Label: "Breadcrumb",
		Crumbs: []widget.Crumb{
			{Label: "Home", Href: "/"},
			{Label: "Phones", Href: "/c/phones"},
			{Label: "Pixelight 9"},
		},
	}).Render(t.Context(), &b)
	if err != nil {
		t.Fatal(err)
	}
	html := b.String()
	if got := strings.Count(html, `aria-current="page"`); got != 1 {
		t.Fatalf("aria-current appears %d times, want 1:\n%s", got, html)
	}
	tail := regexp.MustCompile(`<a class="ui-crumbs__link" href="/c/phones">Phones</a><span class="ui-crumbs__sep"[^>]*>/</span>\s*<span class="ui-crumbs__current" aria-current="page">Pixelight 9</span></nav>$`)
	if !tail.MatchString(html) {
		t.Errorf("the trail does not end with its parent link and the current page:\n%s", html)
	}
}
