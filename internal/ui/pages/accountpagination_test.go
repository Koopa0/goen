package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

func TestEmptyOlderOrdersPageOffersLatestOrders(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{OrdersBound: web.Bound{PastEnd: true, First: "/account#orders-heading"}}))
	if !strings.Contains(body, `href="/account#orders-heading"`) || !strings.Contains(body, "There are no entries on this page.") {
		t.Fatal("empty older page lost its return to latest orders")
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyNoOrdersYet)) {
		t.Fatal("empty later page incorrectly claims the customer has never ordered")
	}
}

func TestAnEmptyFirstOrdersPageStillSaysNoOrdersYet(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{}))
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyNoOrdersYet)) {
		t.Fatal("a customer with no orders lost the no-orders sentence")
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) {
		t.Fatal("an empty first page shows the later-page sentence")
	}
}
