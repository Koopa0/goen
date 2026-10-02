package admin

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

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

	admin := renderComponent(t, ctx, Dashboard(layouts.Page{}, DashboardView{}))
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

	shop := renderComponent(t, ctx, pages.Account(pages.AccountMeta(ctx), &pages.AccountView{Email: "somebody@example.com"}))
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
		"coupons":    Coupons(layouts.Page{}, CouponsView{}),
		"newsletter": Newsletter(layouts.Page{}, NewsletterView{}),
		"tiers":      Tiers(layouts.Page{}, TiersView{}),
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
