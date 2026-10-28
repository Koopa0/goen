package warranties

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// renderComponent renders c under ctx, which the caller owns so the locale
// stays explicit at the call site.
func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// Meta is the page the warranty tests wrap their views in: the title is not
// what they assert, so a fixed one is enough.
func Meta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyAdminQueueDashboardTitle)}
}
