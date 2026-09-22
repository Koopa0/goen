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

// TestTheDesktopHeaderOffersTheBackOffice covers the width the back office is
// actually used at. The drawer that carries the entrance is display:none from
// 1024px up, so a staff member at a desk saw no door on any page but /account
// — two clicks to reach the screen they open all day.
//
// It takes the WIDE slot rather than adding a fourth button: measured in Chrome,
// the action row at 1024 in English already leaves the search field 52px, and
// anything added there scrolls the page sideways. The slot holds the wishlist
// for a customer, which is why both halves are asserted here.
func TestTheDesktopHeaderOffersTheBackOffice(t *testing.T) {
	t.Parallel()
	base := i18n.WithLocale(t.Context(), i18n.ZhHant)

	staff := renderComponent(t, layouts.WithStaff(base, true), layouts.Header(layouts.Page{}))
	if !strings.Contains(staff, `class="ui-btn ui-btn--ghost ui-btn--icon goen-header__action--wide" href="/admin"`) {
		t.Error("the wide action row offers a staff member no back office, so a " +
			"desktop staff member reaches it only through /account")
	}

	customer := renderComponent(t, base, layouts.Header(layouts.Page{}))
	if !strings.Contains(customer, `href="/account/wishlist"`) {
		t.Error("the wide slot lost the customer's wishlist")
	}
	if strings.Contains(customer, `href="/admin"`) {
		t.Error("the wide slot offers a customer the back office")
	}
}

// storefrontChrome is what the shop's document shell renders for a shopper and
// the back office has no use for, named by the markup each one produces: the
// header's product search and the footer's newsletter signup. Naming them by a
// class would leave this test green on a page that still shipped the form under
// a class the back office never mentions.
var storefrontChrome = map[string]string{
	"the footer's newsletter signup": `id="newsletter-form"`,
	"the header's product search":    `action="/search"`,
}

// TestTheBackOfficeRendersNoStorefrontChrome holds the two shells apart. Every
// admin screen used to come through the storefront's, so a staff member working
// a queue was shown a category row, a product search, a wishlist, a cart and a
// newsletter signup on every page — and a newsletter form on /admin is not only
// noise, it is a second POST target on a screen that exists to take writes.
//
// The storefront half of the assertion is the half that matters in a year: a
// shell that stopped rendering the search and the signup everywhere would
// otherwise pass this as a back-office win.
func TestTheBackOfficeRendersNoStorefrontChrome(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	admin := renderComponent(t, ctx, AdminDashboard(layouts.Page{}, AdminDashboardView{}))
	// Without this the loop below passes on an empty render, which is the false
	// green a page that failed to build gives.
	if !strings.Contains(admin, `class="goen-admin"`) {
		t.Fatal("the dashboard did not render its own frame; the assertions " +
			"below would pass on anything")
	}
	for where, markup := range storefrontChrome {
		if strings.Contains(admin, markup) {
			t.Errorf("the back office still ships %s: %s is in the page", where, markup)
		}
	}
	if !strings.Contains(admin, `<form method="post" action="/signout">`) {
		t.Error("the back office's own bar offers no way to sign out, so the " +
			"chrome it replaced took the only one with it")
	}

	shop := renderAccountPage(t, ctx)
	for where, markup := range storefrontChrome {
		if !strings.Contains(shop, markup) {
			t.Errorf("the storefront lost %s: no %s in the page", where, markup)
		}
	}
}

// TestEveryAdminScreenPutsItsWorkInTheContentColumn holds the frame at the one
// place that can hold it. The rail is a column beside the work from 1024 up,
// and the rule that puts it there reaches it through the element the work is
// wrapped in — so a screen that opened .goen-admin itself and dropped its
// sections straight into it gave the grid extra children, and the rail landed
// on a row above them instead. Four screens had the wrapper and nineteen did
// not, which is why the back office looked like two different products.
//
// The three below are screens the rebuild has not reached, chosen for that
// reason: they are the ones that were wrong, and they get the frame now from
// the shell rather than from anything they write themselves.
func TestEveryAdminScreenPutsItsWorkInTheContentColumn(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	for name, screen := range map[string]templ.Component{
		"coupons":    AdminCoupons(layouts.Page{}, AdminCouponsView{}),
		"newsletter": AdminNewsletter(layouts.Page{}, AdminNewsletterView{}),
		"tiers":      AdminTiers(layouts.Page{}, AdminTiersView{}),
	} {
		html := renderComponent(t, ctx, screen)

		frame := strings.Index(html, `<div class="goen-admin">`)
		rail := strings.Index(html, "goen-admin__nav")
		column := strings.Index(html, `<div class="goen-admin__main">`)
		work := strings.Index(html, `class="ui-page-head"`)
		if frame < 0 || rail < 0 || column < 0 || work < 0 {
			t.Fatalf("%s: rendered no back-office frame at all (frame=%d rail=%d "+
				"column=%d work=%d); the order below would prove nothing",
				name, frame, rail, column, work)
		}
		if frame > rail || rail > column || column > work {
			t.Errorf("%s: the frame reads frame=%d rail=%d column=%d work=%d — the "+
				"work has to open INSIDE the content column, or it is a sibling of "+
				"the rail and the grid puts the rail on a row of its own",
				name, frame, rail, column, work)
		}
		if n := strings.Count(html, `<div class="goen-admin__main">`); n != 1 {
			t.Errorf("%s: %d content columns, want exactly 1 — a second one is a "+
				"second grid child and the same defect one level down", name, n)
		}
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
