package home

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/tone"
)

func renderIn(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestHomeDepartmentCardsWearTheirToneAndPhotograph(t *testing.T) {
	t.Parallel()
	page := renderIn(t, i18n.ZhHant, Page(layouts.Page{}, View{Categories: []Category{
		{Slug: "tech", Name: "3C 數位", Tone: tone.Mist, Photo: components.Photo{URL: "/static/media/products/department-tech.webp", Srcset: "/static/x-400.webp 400w"}},
		{Slug: "unlisted", Name: "其他", Tone: tone.Stone},
	}}))
	if !strings.Contains(page, `data-tone="mist" href="/c/tech"`) || !strings.Contains(page, "3C 數位") {
		t.Fatal("the card lost its tone, link or name")
	}
	if !strings.Contains(page, `alt="" width="800" height="600"`) {
		t.Error("a card photograph must carry an empty alt: the name beside it is its label")
	}
	if strings.Count(page, `class="goen-cat__photo"`) != 1 {
		t.Error("a department with no photograph must draw none, and only that one")
	}
}
