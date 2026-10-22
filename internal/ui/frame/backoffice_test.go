package frame_test

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// backOfficeEntrances is every door into /admin the storefront renders, by the
// markup each one produces: the header's drawer below 1024, and the account
// page at every width. A test naming one of them would go green while the other
// disappeared.
//
// The wide bar's action row is deliberately not one of them, and header.templ
// carries the measurement: at 1024 the English header has no room left.
var backOfficeEntrances = map[string]string{
	"the header's drawer":    `<a class="ui-navitem" href="/admin">`,
	"the account page's nav": `<a class="goen-account__navitem" href="/admin">`,
}

// TestHeaderBackOfficeEntrance holds where the entrance LIVES. It
// is the header, so it reaches a staff member on whatever page they are on —
// putting it only on /account would make the shop's own catalogue the long way
// round to the page that edits it.
func TestHeaderBackOfficeEntrance(t *testing.T) {
	t.Parallel()
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)

	header := renderComponent(t, frame.WithStaff(base, true), frame.Header(frame.Page{}))
	if !strings.Contains(header, backOfficeEntrances["the header's drawer"]) {
		t.Error("the site header offers no back office in its drawer")
	}

	if customer := renderComponent(t, base, frame.Header(frame.Page{})); strings.Contains(customer, "/admin") {
		t.Error("the site header offers a customer the back office")
	}
}

// TestHeaderDesktopBackOfficeLink asserts what the bar itself offers, not
// the drawer's copy: a staff member sees a visible text link to /admin with no
// aria-label to override it, and both staff and customer keep the wishlist heart.
func TestHeaderDesktopBackOfficeLink(t *testing.T) {
	t.Parallel()
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)
	const wishlist = `goen-header__action--wide" href="/account/wishlist"`

	staff := renderComponent(t, frame.WithStaff(base, true), frame.Header(frame.Page{}))
	if !strings.Contains(staff, `class="ui-btn ui-btn--ghost goen-header__backoffice" href="/admin">`+i18n.T(base, i18n.KeyBackOffice)+`</a>`) {
		t.Error("the bar offers a staff member no visible-text back office link, so a " +
			"desktop staff member reaches it only through /account")
	}
	if strings.Contains(staff, `goen-header__backoffice" href="/admin" aria-label`) {
		t.Error("the staff entrance has an aria-label that overrides its visible text")
	}
	if !strings.Contains(staff, wishlist) {
		t.Error("the bar lost the wishlist heart for a staff member")
	}

	customer := renderComponent(t, base, frame.Header(frame.Page{}))
	if !strings.Contains(customer, wishlist) {
		t.Error("the bar lost the customer's wishlist heart")
	}
	if strings.Contains(customer, `href="/admin"`) {
		t.Error("the bar offers a customer the back office")
	}
}

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
