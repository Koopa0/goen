package frame

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// TestOrderNotFoundRecoveryActions: order not found offers recovery for the visitor.
func TestOrderNotFoundRecoveryActions(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, signedIn := range []bool{false, true} {
			ctx := i18n.WithLocale(t.Context(), locale)
			if signedIn {
				ctx = user.NewContext(ctx, user.User{ID: "customer"})
			}
			body := renderNotice(t, OrderNotFound(Page{}), ctx)
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

// TestNoticeDepartmentsOnNotFound: not found lists the departments and a server error does not.
func TestNoticeDepartmentsOnNotFound(t *testing.T) {
	t.Parallel()
	ctx := WithTopNav(i18n.WithLocale(t.Context(), i18n.En), []NavItem{
		{Slug: "phones", Name: "Phones", Href: "/c/phones"},
		{Slug: "laptops", Name: "Laptops", Href: "/c/laptops"},
	})
	page := Page{Title: "test"}
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

// TestNoticeNextStepOnNotFound: a missing page's way back to the shop is the
// section's one blue next-step link with its arrow, and the page draws no solid
// button; its departments are the shared index, whose photograph link is out of
// the tab order and hidden beside a named twin. A page that names its own ways
// back (an order that is not the visitor's) keeps them as buttons.
func TestNoticeNextStepOnNotFound(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := WithTopNav(i18n.WithLocale(t.Context(), locale), []NavItem{
			{Slug: "tech", Name: "3C", Href: "/c/tech", PhotoURL: "/static/tech.webp"},
		})
		body := renderNotice(t, Notice(Page{Title: "x"}, "404", "Not found", "Gone"), ctx)
		_, notice, ok := strings.Cut(body, `<div class="notice notice--missing">`)
		if !ok {
			t.Fatalf("%s: the 404 is not drawn as a missing page", locale)
		}
		notice, _, _ = strings.Cut(notice, "<footer")
		want := `<a class="notice__next" href="/">` + i18n.T(ctx, i18n.KeyBackToShop) + `<span aria-hidden="true">&nbsp;→</span></a>`
		if !strings.Contains(notice, want) {
			t.Errorf("%s: the 404's way back is not %s", locale, want)
		}
		if strings.Contains(notice, "goen-btn") {
			t.Errorf("%s: the 404 draws a button", locale)
		}
		for _, want := range []string{
			`<a class="goen-deptindex__photo" href="/c/tech" tabindex="-1" aria-hidden="true">`,
			`<h3 class="goen-deptindex__name"><a href="/c/tech">3C</a></h3>`,
		} {
			if !strings.Contains(notice, want) {
				t.Errorf("%s: the 404's departments lack %s", locale, want)
			}
		}
		order := renderNotice(t, OrderNotFound(Page{}), ctx)
		if strings.Contains(order, "notice--missing") || !strings.Contains(order, `goen-btn goen-btn--primary`) {
			t.Errorf("%s: the missing order lost its own ways back", locale)
		}
	}
}
