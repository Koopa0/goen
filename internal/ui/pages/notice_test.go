package pages

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
)

func TestOrderNotFoundOffersRecoveryForTheVisitor(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, signedIn := range []bool{false, true} {
			ctx := i18n.WithLocale(t.Context(), locale)
			if signedIn {
				ctx = user.NewContext(ctx, user.User{ID: "customer"})
			}
			body := renderNotice(t, OrderNotFound(layouts.Page{}), ctx)
			if !strings.Contains(body, `href="/orders/find"`) {
				t.Error("missing find-order link")
			}
			// The header can offer sign-in too; inspect only the notice actions.
			_, actions, ok := strings.Cut(body, `class="notice__actions"`)
			if !ok {
				t.Fatal("missing notice actions")
			}
			actions, _, _ = strings.Cut(actions, "</div>")
			if got := strings.Contains(actions, `href="/signin"`); got == signedIn {
				t.Errorf("%s signedIn=%t: sign-in action present=%t", locale, signedIn, got)
			}
			if strings.Contains(body, `class="notice__code"`) {
				t.Error("missing-order page still displays a status code")
			}
			if signedIn && locale == i18n.ZhHant && !strings.Contains(body, "這筆訂單不在你的帳號裡，請用訂單編號和電子郵件查詢。") {
				t.Error("signed-in visitor is not told to use the order number and email")
			}
		}
	}
}

func renderNotice(t *testing.T, c templ.Component, ctx context.Context) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func TestNotFoundListsTheDepartmentsAndAServerErrorDoesNot(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithTopNav(i18n.WithLocale(t.Context(), i18n.En), []layouts.NavItem{
		{Slug: "phones", Name: "Phones", Href: "/c/phones"},
		{Slug: "laptops", Name: "Laptops", Href: "/c/laptops"},
	})
	page := layouts.Page{Title: "test"}
	missing := renderNotice(t, Notice(page, "404", "Not found", "Gone"), ctx)
	for _, want := range []string{`class="notice__departments"`, `href="/c/phones"`, `href="/c/laptops"`, ">Laptops<"} {
		if !strings.Contains(missing, want) {
			t.Errorf("the 404 lacks %q", want)
		}
	}
	broken := renderNotice(t, Notice(page, "500", "Broken", "Try again"), ctx)
	if strings.Contains(broken, "notice__departments") {
		t.Error("a 500 lists departments; only a missing page sends the visitor elsewhere")
	}
	bare := renderNotice(t, Notice(page, "404", "Not found", "Gone"), i18n.WithLocale(t.Context(), i18n.En))
	if strings.Contains(bare, "notice__departments") {
		t.Error("a 404 outside the middleware draws an empty department list")
	}
}
