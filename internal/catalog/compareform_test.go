package catalog

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
)

// TestCompareFormSubmitIsTonal: comparing the ticked products is a step forward, not the
// listing's own action, so its submit is the tonal level.
func TestCompareFormSubmitIsTonal(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, compareForm(true))))
			if err != nil {
				t.Fatal(err)
			}
			submit := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
			})
			if submit == nil {
				t.Fatal("the compare bar has no submit")
			}
			if class := attrValue(submit, "class"); !hasClass(submit, "goen-btn--secondary") || hasClass(submit, "goen-btn--outline") {
				t.Errorf("the compare submit is drawn as %q, want the tonal level", class)
			}
		})
	}
}

// TestCompareFormWithoutScript holds the compare bar as it is with no script:
// a GET to /compare around the grid, each tile's box a field of it, and at its
// tail the limit, then the submit. The row of ticked products is goen.js's, so
// the server never renders one.
func TestCompareFormWithoutScript(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			cell := tile.Cell(tile.Tile{Slug: "nimbus-speaker-s1", Name: "Nimbus Speaker S1", PriceCents: 249000, InStock: true, Comparable: true})
			doc, err := html.Parse(strings.NewReader(renderComponent(t, templ.WithChildren(ctx, cell), compareForm(true))))
			if err != nil {
				t.Fatal(err)
			}
			form := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "form" })
			if form == nil {
				t.Fatal("compareForm(true) renders no form")
			}
			if got := [3]string{attrValue(form, "id"), attrValue(form, "method"), attrValue(form, "action")}; got != [3]string{"compare-pick", "get", "/compare"} {
				t.Errorf("compareForm(true) form id, method, action = %q, want compare-pick, get, /compare", got)
			}

			box := findDescendant(form, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "input" })
			switch {
			case box == nil:
				t.Error("the grid inside the form has no compare box")
			case attrValue(box, "type") != "checkbox" || attrValue(box, "name") != "p" || attrValue(box, "value") != "nimbus-speaker-s1" || attrValue(box, "form") != "compare-pick":
				t.Errorf("the compare box is %v, want a checkbox p=nimbus-speaker-s1 of form compare-pick", box.Attr)
			case box.Parent == nil || box.Parent.Data != "label" || !hasClass(box.Parent, "goen-tile__compare"):
				t.Error("the compare box is not inside its label.goen-tile__compare")
			case !strings.HasPrefix(strings.TrimSpace(nodeText(box.Parent)), i18n.T(ctx, i18n.KeyCompareAdd)):
				t.Errorf("the compare label reads %q, want it to start with %q", nodeText(box.Parent), i18n.T(ctx, i18n.KeyCompareAdd))
			}

			var bar *html.Node
			for c := form.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && hasClass(c, "goen-compare-pick__go") {
					bar = c
				}
			}
			if bar == nil {
				t.Fatal("the form has no bar (.goen-compare-pick__go) of its own")
			}
			var parts []string
			for c := bar.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode {
					continue
				}
				switch {
				case hasClass(c, "goen-compare-pick__note"):
					parts = append(parts, "note:"+strings.TrimSpace(nodeText(c)))
				case c.Data == "button" && attrValue(c, "type") == "submit":
					parts = append(parts, "submit:"+strings.TrimSpace(nodeText(c)))
				default:
					parts = append(parts, c.Data+"."+className(c))
				}
			}
			want := []string{
				"note:" + fmt.Sprintf(i18n.T(ctx, i18n.KeyCompareLimit), MaxCompare),
				"submit:" + i18n.T(ctx, i18n.KeyCompareSelected),
			}
			if !slices.Equal(parts, want) {
				t.Errorf("the bar holds %q, want %q", parts, want)
			}
		})
	}
}

// TestCompareFormNotOffered: with no box to tick there is no form, only the grid.
func TestCompareFormNotOffered(t *testing.T) {
	t.Parallel()
	ctx := templ.WithChildren(i18n.WithLocale(t.Context(), i18n.En), tile.Cell(tile.Tile{Slug: "gift-card", Name: "Gift card"}))
	page := renderComponent(t, ctx, compareForm(false))
	if strings.Contains(page, "<form") || strings.Contains(page, "goen-compare-pick") {
		t.Errorf("compareForm(false) renders a compare form: %s", page)
	}
	if !strings.Contains(page, `href="/p/gift-card"`) {
		t.Errorf("compareForm(false) drops the grid: %s", page)
	}
}
