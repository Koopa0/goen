package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestARefusedProductFormKeepsTheNoBrandChoice(t *testing.T) {
	for _, tc := range []struct {
		locale i18n.Locale
		label  string
	}{{locale: i18n.ZhHant, label: "無品牌"}, {locale: i18n.En, label: "No brand"}} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		view := ProductView{Slug: "generic", Name: "Generic", Brands: []Choice{{Value: "maker-id", Label: "Maker"}}, Errors: map[string]string{"name": "refused"}}
		body := renderComponent(t, ctx, ProductForm(layouts.Page{Title: "Product"}, view))
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var selectNode *html.Node
		var visit func(*html.Node)
		visit = func(n *html.Node) {
			for _, attr := range n.Attr {
				if n.Data == "select" && attr.Key == "id" && attr.Val == "p-brand" {
					selectNode = n
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				visit(child)
			}
		}
		visit(doc)
		if selectNode == nil {
			t.Fatal("missing brand select")
		}
		for _, attr := range selectNode.Attr {
			if attr.Key == "required" {
				t.Error("no brand is a valid choice but the select remains required")
			}
		}
		var noBrand *html.Node
		for child := selectNode.FirstChild; child != nil; child = child.NextSibling {
			if child.Data == "option" {
				for _, attr := range child.Attr {
					if attr.Key == "value" && attr.Val == "" {
						noBrand = child
					}
				}
			}
		}
		if noBrand == nil || noBrand.FirstChild == nil || noBrand.FirstChild.Data != tc.label {
			t.Errorf("%s: missing explicit no-brand option %q", tc.locale, tc.label)
		}
	}
}
