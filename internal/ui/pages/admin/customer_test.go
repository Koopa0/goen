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
