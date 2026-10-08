package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
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

// TestOnlyAStaffMemberIsOfferedTheBackOffice holds both halves of the entrance.
// The back office had no link from anywhere on the site and the only way in was
// typing the URL; the fix must not become the opposite defect, which is a
// customer being offered a door that answers 404 — RequireStaff does not
// disclose that /admin exists, so following it is a dead end that also says the
// shop has something to hide.
func TestOnlyAStaffMemberIsOfferedTheBackOffice(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			base := i18n.WithLocale(t.Context(), locale)
			staff := renderAccountPage(t, layouts.WithStaff(base, true))

			for where, markup := range backOfficeEntrances {
				if !strings.Contains(staff, markup) {
					t.Errorf("a staff member is offered no back office in %s: no %s in the page",
						where, markup)
				}
			}

			// The label comes off the catalogue, so a staff member reading in
			// English does not meet the one Chinese item on an English page.
			label := i18n.T(base, i18n.KeyBackOffice)
			if !strings.Contains(staff, ">"+label+"<") {
				t.Errorf("the back-office entrance does not render %q in %s", label, locale.Tag())
			}
			if !strings.Contains(staff, i18n.T(base, i18n.KeyBackOfficeHint)) {
				t.Errorf("the account page's back-office entrance renders no hint in %s", locale.Tag())
			}

			// Both shapes of "not staff": the middleware never ran, and it ran
			// and said no. Only the second survives a refactor that starts
			// setting the flag unconditionally.
			for name, ctx := range map[string]context.Context{
				"no middleware": base,
				"not staff":     layouts.WithStaff(base, false),
			} {
				customer := renderAccountPage(t, ctx)
				// Without this the assertion below passes on an empty render,
				// which is the false green a page that failed to build gives.
				if !strings.Contains(customer, `href="/account/warranty"`) {
					t.Fatalf("%s: the account page did not render its own nav; the "+
						"assertion below would pass on anything", name)
				}
				if strings.Contains(customer, "/admin") {
					t.Errorf("%s: a customer's page links into the back office", name)
				}
			}
		})
	}
}

// TestTheBackOfficeEntranceIsSiteWideChrome holds where the entrance LIVES. It
// is the header, so it reaches a staff member on whatever page they are on —
// putting it only on /account would make the shop's own catalogue the long way
// round to the page that edits it.
func TestTheBackOfficeEntranceIsSiteWideChrome(t *testing.T) {
	t.Parallel()
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)

	header := renderComponent(t, layouts.WithStaff(base, true), layouts.Header(layouts.Page{}))
	if !strings.Contains(header, backOfficeEntrances["the header's drawer"]) {
		t.Error("the site header offers no back office in its drawer")
	}

	if customer := renderComponent(t, base, layouts.Header(layouts.Page{})); strings.Contains(customer, "/admin") {
		t.Error("the site header offers a customer the back office")
	}
}

// TestTheDesktopHeaderOffersTheBackOffice asserts what the bar itself offers, not
// the drawer's copy: a staff member sees a visible text link to /admin with no
// aria-label to override it, and both staff and customer keep the wishlist heart.
func TestTheDesktopHeaderOffersTheBackOffice(t *testing.T) {
	t.Parallel()
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)
	const wishlist = `goen-header__action--wide" href="/account/wishlist"`

	staff := renderComponent(t, layouts.WithStaff(base, true), layouts.Header(layouts.Page{}))
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

	customer := renderComponent(t, base, layouts.Header(layouts.Page{}))
	if !strings.Contains(customer, wishlist) {
		t.Error("the bar lost the customer's wishlist heart")
	}
	if strings.Contains(customer, `href="/admin"`) {
		t.Error("the bar offers a customer the back office")
	}
}

func renderAccountPage(t *testing.T, ctx context.Context) string {
	t.Helper()
	return renderComponent(t, ctx, Account(
		AccountMeta(ctx),
		&AccountView{Email: "somebody@example.com"},
	))
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
