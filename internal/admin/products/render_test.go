package products

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The helpers below are copies of the render helpers every page package's
// tests keep, rather than exported ones: a test cannot import another's.

// renderComponent renders c under ctx, which the caller owns so the locale and
// the staff flag stay explicit at the call site.
func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// Meta is the page the tests here wrap their views in: the title is not what
// they assert, so a fixed one is enough.
func Meta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

// firstPageBound is the bound the stores build for the first page of a
// queue that has no rows, exactly as production builds it.
func firstPageBound(scope string) keyset.Bound {
	_, b := keyset.PageBound(scope, false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	return b
}

// fullPageBound is the first page of a queue with more rows behind it.
func fullPageBound(scope string) keyset.Bound {
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound(scope, false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	return b
}

func attr(n *html.Node, key string) string {
	v, _ := attrPresent(n, key)
	return v
}

func attrPresent(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}
