package products

import (
	"bytes"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests in this file are the products halves of tests that render several
// desks' pages: each half lives with the page it renders.

// TestProductsEmptyRestartLink: the products page keeps its restart link when a
// page past the end is empty.
//
// From TestAuditEmptyPageRestartLink.
func TestProductsEmptyRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	p := frame.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"products": Products(p, ProductsView{Bound: b}),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
				t.Fatal("empty page lost its plain restart link")
			}
			if strings.Count(body, "There are no entries on this page.") != 1 {
				t.Fatal("empty page lost its shared empty state")
			}
		})
	}
}

// queuePages builds the products list's page around one bound, with no rows.
func queuePages(b keyset.Bound, ctx func(k i18n.Key) string) map[string]struct {
	page templ.Component
	want string
} {
	p := frame.Page{Title: "Queue"}
	type c = struct {
		page templ.Component
		want string
	}
	return map[string]c{
		"products": {Products(p, ProductsView{Bound: b}), ctx(i18n.KeyAdminProdEmpty)},
	}
}

// TestProductsFirstPageEmptyState: the products queue keeps its own empty state
// on an empty first page.
//
// From TestAuditFirstPageEmpty.
func TestProductsFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	for name, c := range queuePages(firstPageBound("/admin/queue"), ctx) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, html.EscapeString(c.want)) {
				t.Errorf("the queue's own empty text %q is missing", c.want)
			}
			if strings.Contains(body, generic) {
				t.Error("an empty first page shows the later-page sentence")
			}
		})
	}
}

// TestProductsNextLink: a products queue with more rows offers a Next link.
//
// From TestAuditFullPageOffersNext.
func TestProductsNextLink(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	for name, c := range queuePages(fullPageBound("/admin/queue"), ctx) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, `rel="next"`) {
				t.Error("a first page with more rows has no Next link")
			}
			if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
				t.Error("a first page with more rows says it is empty")
			}
		})
	}
}
