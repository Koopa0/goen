package pages

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestTheOrderNotFoundPageOffersAWayThrough holds that this 404 offers both
// /orders/find and /signin, since the reader is a guest or an account holder
// and the page cannot tell which.
func TestTheOrderNotFoundPageOffersAWayThrough(t *testing.T) {
	html := renderToString(t, layouts.OrderNotFound(layouts.Page{Title: "404"}))

	for _, want := range []string{"/orders/find", "/signin"} {
		if !strings.Contains(html, want) {
			t.Errorf("the order 404 does not link to %s; the reader is one of two "+
				"people and the page cannot tell which", want)
		}
	}
}

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
