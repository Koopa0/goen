package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestHomeShowsTheErasureResultOnlyWhenRequested(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, erased := range []bool{false, true} {
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Home(HomeMeta(ctx), HomeView{AccountErased: erased}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			notice := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == "account-erased" })
			if erased {
				if notice == nil || attrValue(notice, "role") != "status" || nodeText(notice) != i18n.T(ctx, i18n.KeyAccountErased) {
					t.Errorf("%s home lacks the erasure status", locale.Tag())
				}
			} else if notice != nil || strings.Contains(nodeText(doc), i18n.T(ctx, i18n.KeyAccountErased)) {
				t.Errorf("%s ordinary home claims an account was erased", locale.Tag())
			}
		}
	}
}
