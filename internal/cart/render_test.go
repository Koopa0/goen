package cart

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

// The ui/pages test helpers the moved cart and checkout view tests call, copied
// with the bodies ui/pages keeps: a test in another package cannot import them.

func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	// The context is kept at the call site so the locale and the staff flag are
	// both explicit: this test is about what each one puts on the page.
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func renderIn(t *testing.T, locale i18n.Locale, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
