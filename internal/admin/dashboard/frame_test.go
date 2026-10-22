package dashboard

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
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

// TestDashboardNoStorefrontChrome holds the two shells apart. Every
// admin screen used to come through the storefront's, so a staff member working
// a queue was shown a category row, a product search, a wishlist, a cart and a
// newsletter signup on every page — and a newsletter form on /admin is not only
// noise, it is a second POST target on a screen that exists to take writes.
//
// The storefront half of the assertion is the half that matters in a year: a
// shell that stopped rendering the search and the signup everywhere would
// otherwise pass this as a back-office win.
func TestDashboardNoStorefrontChrome(t *testing.T) {
	t.Parallel()
	ctx := frame.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	admin := renderComponent(t, ctx, Dashboard(frame.Page{}, DashboardView{}))
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

	shop := renderComponent(t, ctx, account.Account(account.Meta(ctx), &account.View{Email: "somebody@example.com"}))
	for where, markup := range storefrontChrome {
		if !strings.Contains(shop, markup) {
			t.Errorf("the storefront lost %s: no %s in the page", where, markup)
		}
	}
}
