package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestTheGalleryPicksWithoutScript holds the shape the colour swap and the CSS
// both read: the radios and their labels are in the markup, and the first <img>
// in #gallery is a stage shot. The layout gate reads `#gallery img` as the
// photograph the page opens on, so a thumbnail ahead of it would pass every
// other check and swap the wrong photograph.
func TestTheGalleryPicksWithoutScript(t *testing.T) {
	t.Parallel()

	view := func(n int) *ProductView {
		v := &ProductView{
			Name: "Pixelight 9 Pro", Brand: "Pixelight", Slug: "pixelight-9-pro",
			SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
			PriceCents: 3690000,
		}
		for i := range n {
			v.Images = append(v.Images, ProductImage{
				URL: "/media/p/" + string(rune('a'+i)) + ".webp", Alt: "shot",
			})
		}
		return v
	}
	render := func(v *ProductView) string {
		html := renderProductInLocale(t, i18n.WithLocale(t.Context(), i18n.ZhHant), v)
		start := strings.Index(html, `id="gallery"`)
		end := strings.Index(html, `id="buybox"`)
		if start < 0 || end <= start {
			t.Fatal("the page has no gallery before its buy column")
		}
		return html[start:end]
	}

	t.Run("three shots", func(t *testing.T) {
		t.Parallel()
		g := render(view(3))
		if n := strings.Count(g, `name="shot"`); n != 3 {
			t.Errorf("radios = %d, want 3", n)
		}
		if n := strings.Count(g, ` checked`); n != 1 || !strings.Contains(g[:strings.Index(g, `id="shot-1"`)], ` checked`) {
			t.Errorf("want exactly the first radio checked, got %d checked", n)
		}
		for _, id := range []string{"shot-0", "shot-1", "shot-2"} {
			if !strings.Contains(g, `for="`+id+`"`) {
				t.Errorf("no label points at %s", id)
			}
		}
		first := regexp.MustCompile(`<img class="([^"]+)"`).FindStringSubmatch(g)
		if len(first) < 2 || first[1] != "goen-pdp__shotimg" {
			t.Errorf("the first <img> in #gallery has class %q, want goen-pdp__shotimg", first)
		}
	})

	t.Run("one shot", func(t *testing.T) {
		t.Parallel()
		g := render(view(1))
		if strings.Contains(g, `type="radio"`) || strings.Contains(g, "<label") {
			t.Error("a single photograph offers a choice between itself")
		}
		if !strings.Contains(g, "goen-pdp__shotimg") {
			t.Error("the single photograph is missing")
		}
	})
}
