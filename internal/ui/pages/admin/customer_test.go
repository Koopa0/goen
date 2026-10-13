package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestACustomerShowsItsFiguresAsOneStatLine(t *testing.T) {
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

func TestCustomerSpendAndCreditLeadToTheirRecords(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := &CustomerView{ID: "11111111-2222-3333-4444-555555555555", Email: "ada+credit@example.test", SpentCents: 12300, CreditCents: 4500}
		var rendered strings.Builder
		if err := Customer(Meta(ctx), v).Render(ctx, &rendered); err != nil {
			t.Fatal(err)
		}
		body := rendered.String()
		label := "已成立訂單金額（扣除退款）"
		if locale == i18n.En {
			label = "Confirmed orders, net of refunds"
		}
		for _, want := range []string{label, `href="/admin/credit?customer=` + v.ID + `"`, `href="/admin/credit?customer=` + v.ID + `#credit-email"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s profile lacks %q", locale.Tag(), want)
			}
		}
	}
}
