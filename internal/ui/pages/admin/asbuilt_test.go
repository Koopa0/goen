package admin

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestTheCouponFormSaysTheLimitIsPerMemberAccount(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Coupons(layouts.Page{Title: "c"}, CouponsView{}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		for _, k := range []i18n.Key{i18n.KeyAdminCoupPerCustomer, i18n.KeyAdminCoupPerCustomerHint} {
			if !strings.Contains(b.String(), i18n.T(ctx, k)) {
				t.Errorf("%v coupon form lacks %q", loc, i18n.T(ctx, k))
			}
		}
	}
}

func TestOnlyAPickupOrderIsToldToCompleteAfterCollection(t *testing.T) {
	t.Parallel()
	next := []Transition{{Value: pages.FulfillmentCompleted, Label: "x"}}
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		want := i18n.T(ctx, i18n.KeyAdminQueuePickupCompleteHint)
		render := func(pickup bool) string {
			var b strings.Builder
			v := &OrderView{Number: "GO-1", Status: "shipped", Next: next, PickupDestination: pickup}
			if err := Order(layouts.Page{Title: "o"}, v).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			return b.String()
		}
		if !strings.Contains(render(true), want) {
			t.Errorf("%v: a pickup order is not told when to complete it", loc)
		}
		if strings.Contains(render(false), want) {
			t.Errorf("%v: a home-delivery order is told about store collection", loc)
		}
	}
}

func TestTheAllowanceActionSaysThePaperConfirmationIsTheShops(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		v := &OrderView{
			Number: "GO-260721-000387", Status: "completed", Committed: true, InvoicingEnabled: true, RefundedCents: 84900,
			InvoiceDocuments: []InvoiceDocument{{Kind: "invoice", Number: "AA12345678", Status: "issued", AmountCents: 100000}},
		}
		if err := Order(layouts.Page{Title: "o"}, v).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), html.EscapeString(i18n.T(ctx, i18n.KeyAdminQueueAllowancePaper))) {
			t.Errorf("%v: the allowance action does not say the shop keeps the signed confirmation", loc)
		}
	}
}
