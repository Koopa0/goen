package cart

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestPageNoticeLooksLikeItsOutcome: an adjusted quantity is announced in the
// accent style and a variant that sold out in the danger style, and a reorder
// that put nothing back is a refusal while one that put something back is not.
func TestPageNoticeLooksLikeItsOutcome(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		query string
		view  View
		class string
		role  string
	}{
		{"adjusted", "qty=adjusted", View{}, "goen-notice--accent", "status"},
		{"unavailable", "qty=unavailable", View{}, "goen-notice--danger", "alert"},
		{"reorder all", "", View{ReorderAdded: 2}, "goen-notice--accent", "status"},
		{"reorder partial", "", View{ReorderAdded: 1, ReorderSkipped: 1}, "goen-notice--accent", "status"},
		{"reorder adjusted", "", View{ReorderSkipped: 1, ReorderAdjusted: true}, "goen-notice--accent", "status"},
		{"reorder none", "", View{ReorderSkipped: 2}, "goen-notice--danger", "alert"},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(tt.name+"/"+locale.Tag(), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := tt.view
				want := ""
				if tt.query != "" {
					req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart?"+tt.query, http.NoBody)
					view.Notice = cartPageNotice(req)
					want = view.Notice.Text
				} else {
					want = view.ReorderText(ctx)
				}
				if want == "" {
					t.Fatal("the notice says nothing")
				}
				body := renderComponent(t, ctx, Page(frame.Page{}, view))
				doc, err := nethtml.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var class, role string
				var found bool
				var walk func(*nethtml.Node)
				walk = func(n *nethtml.Node) {
					if n.Type == nethtml.ElementNode && n.Data == "p" && strings.Contains(attr(n, "class"), "goen-notice") && strings.TrimSpace(text(n)) == want {
						class, role, found = attr(n, "class"), attr(n, "role"), true
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
				}
				walk(doc)
				if !found {
					t.Fatalf("the page shows no notice reading %q", want)
				}
				if !strings.Contains(" "+class+" ", " "+tt.class+" ") || role != tt.role {
					t.Errorf("notice %q has class %q and role %q, want %s and %s", want, class, role, tt.class, tt.role)
				}
			})
		}
	}
}

func attr(n *nethtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func text(n *nethtml.Node) string {
	if n.Type == nethtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}
