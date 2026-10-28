package account

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

// TestAccountNoticeLooksLikeItsOutcome: a change that was saved is announced in
// the accent style and a refused one in the danger style, so "your password is
// wrong" never looks like "your details are saved".
func TestAccountNoticeLooksLikeItsOutcome(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		query string
		class string
		role  string
	}{
		{"saved=1", "goen-notice--accent", "status"},
		{"welcome=1", "goen-notice--accent", "status"},
		{"unlinked=1", "goen-notice--accent", "status"},
		{"other_devices=signed_out", "goen-notice--accent", "status"},
		{"email=sent", "goen-notice--accent", "status"},
		{"password=wrong", "goen-notice--danger", "alert"},
		{"password=invalid", "goen-notice--danger", "alert"},
		{"profile=invalid", "goen-notice--danger", "alert"},
		{"erase=admin", "goen-notice--danger", "alert"},
		{"lastmethod=1", "goen-notice--danger", "alert"},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(tt.query+"/"+locale.Tag(), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?"+tt.query, http.NoBody)
				notice := accountNotice(req)
				if notice.Text == "" {
					t.Fatalf("?%s says nothing", tt.query)
				}
				body := renderComponent(t, ctx, Account(Meta(ctx), &View{Notice: notice}))
				doc, err := nethtml.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				n := findDescendant(doc, func(n *nethtml.Node) bool {
					return n.Type == nethtml.ElementNode && attrValue(n, "id") == "account-notice"
				})
				if n == nil {
					t.Fatalf("?%s renders no account-notice", tt.query)
				}
				if !strings.Contains(" "+attrValue(n, "class")+" ", " "+tt.class+" ") || attrValue(n, "role") != tt.role {
					t.Errorf("?%s notice has class %q and role %q, want %s and %s", tt.query, attrValue(n, "class"), attrValue(n, "role"), tt.class, tt.role)
				}
			})
		}
	}
}
