package customers

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestCustomerStatLine: a customer shows its figures as one stat line.
func TestCustomerStatLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Customer(Meta(ctx), &CustomerView{Orders: 0, SpentCents: 1234500, CreditCents: 0, Points: 12}))
	if n := strings.Count(html, `<dl class="ui-statline ui-statline--wide">`); n != 1 {
		t.Fatalf("Customer drew %d stat lines, want 1", n)
	}
	for _, want := range []string{
		i18n.T(ctx, i18n.KeyAdminCustStatOrders) + `</dt><dd>0`,
		i18n.T(ctx, i18n.KeyAdminCustStatSpent) + `</dt><dd><small class="ui-statline__pre">NT$</small>12,345`,
		i18n.T(ctx, i18n.KeyAdminCustStatPoints) + `</dt><dd>12`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Customer does not carry %q", want)
		}
	}
}

func TestCustomerLinksBalanceAndGrantToCredit(t *testing.T) {
	t.Parallel()
	const id = "11111111-2222-3333-4444-555555555555"
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderComponent(t, ctx, Customer(Meta(ctx), &CustomerView{ID: id, Email: "ada@example.test", SpentCents: 12300, CreditCents: 4500}))
		for name, want := range map[string]string{
			"spend label":          i18n.T(ctx, i18n.KeyAdminCustStatSpent),
			"balance label link":   `<dt><a href="/admin/credits?customer=` + id + `">` + i18n.T(ctx, i18n.KeyAdminCustStatCredit) + `</a></dt>`,
			"grant button to form": `href="/admin/credits?customer=` + id + `#credit-email"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s profile lacks the %s %q", locale.Tag(), name, want)
			}
		}
		if strings.Contains(body, "已完成消費") || strings.Contains(body, "Completed spend") {
			t.Errorf("%s profile still calls confirmed orders completed spend", locale.Tag())
		}
	}
}

func TestCustomerUnverifiedEmailBadge(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	badge := `<span class="goen-badge">` + i18n.T(ctx, i18n.KeyAdminCustEmailUnconfirmed) + `</span>`
	profile := renderComponent(t, ctx, Customer(Meta(ctx), &CustomerView{ID: "c1", Email: "ada@example.test"}))
	list := renderComponent(t, ctx, Customers(Meta(ctx), View{Term: "ada", Searched: true, Rows: []Row{{ID: "c1", Email: "ada@example.test"}}}))
	for name, body := range map[string]string{"profile": profile, "search result": list} {
		if !strings.Contains(body, badge) {
			t.Errorf("%s lacks the neutral unverified badge %q", name, badge)
		}
	}
	verified := renderComponent(t, ctx, Customer(Meta(ctx), &CustomerView{ID: "c1", Email: "ada@example.test", Verified: true}))
	if strings.Contains(verified, "goen-badge") {
		t.Error("a verified customer carries a badge")
	}
}

func TestCustomersShortSearchHint(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderComponent(t, ctx, Customers(Meta(ctx), View{Term: "a"}))
		for name, want := range map[string]string{
			"hint under the box":   `<p class="goen-admin__hint" id="customer-search-hint">` + i18n.T(ctx, i18n.KeyAdminSearchShort) + `</p>`,
			"input names the hint": `aria-describedby="customer-search-hint"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s short search lacks the %s %q", locale.Tag(), name, want)
			}
		}
		if strings.Contains(body, `role="status"`) {
			t.Errorf("%s short search is still announced as a notice", locale.Tag())
		}
		ok := renderComponent(t, ctx, Customers(Meta(ctx), View{Term: "ab", Searched: true}))
		if strings.Contains(ok, "customer-search-hint") {
			t.Errorf("%s search that ran still points at a hint", locale.Tag())
		}
	}
}
