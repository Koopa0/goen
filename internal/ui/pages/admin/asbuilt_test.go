package admin

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
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
	next := []Transition{{Value: order.FulfillmentCompleted, Label: "x"}}
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

// TestTheAllowanceActionSaysTheCustomerAgreesOnline: the 折讓 is ECPay's online
// consent, and an open one says who it waits for until when.
func TestTheAllowanceActionSaysTheCustomerAgreesOnline(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		render := func(v *OrderView) string {
			t.Helper()
			var b strings.Builder
			if err := Order(layouts.Page{Title: "o"}, v).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			return b.String()
		}
		refunded := func() *OrderView {
			return &OrderView{
				Number: "GO-260721-000387", Status: "completed", Committed: true, InvoicingEnabled: true, RefundedCents: 84900,
				InvoiceDocuments: []InvoiceDocument{{Kind: "invoice", Number: "AA12345678", Status: "issued", AmountCents: 100000}},
			}
		}
		if !strings.Contains(render(refunded()), html.EscapeString(i18n.T(ctx, i18n.KeyAdminQueueAllowanceOnline))) {
			t.Errorf("%v: the allowance action does not say the customer agrees online", loc)
		}

		awaiting := refunded()
		awaiting.AllowanceAwaitingUntil = "2026-10-07 09:00"
		got := render(awaiting)
		if !strings.Contains(got, html.EscapeString(fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQueueAllowanceAwaiting), "2026-10-07 09:00"))) {
			t.Errorf("%v: an allowance e-mailed to the customer does not say until when it waits", loc)
		}
		if strings.Contains(got, "/invoice/allowance") {
			t.Errorf("%v: a second allowance is offered while one waits for the customer", loc)
		}

		for _, tt := range []struct {
			category string
			want     string
		}{
			{category: invoice.CategoryBuyerUnconfirmed, want: i18n.T(ctx, i18n.KeyAdminQueueAllowanceUnconfirmed)},
			{category: invoice.CategoryAmountStillHeld, want: i18n.T(ctx, i18n.KeyAdminQueueAllowanceAmountHeld)},
			{category: invoice.CategorySuccessMismatch, want: i18n.T(ctx, i18n.KeyAdminQueueAllowanceMismatch)},
			{category: invoice.CategoryMultipleCandidates, want: i18n.T(ctx, i18n.KeyAdminQueueAllowanceCandidates)},
			{category: "allowance_lookup_mismatch", want: i18n.T(ctx, i18n.KeyAdminQueueAllowanceAttention)},
		} {
			attention := refunded()
			attention.AllowanceAttention = tt.category
			got := render(attention)
			if !strings.Contains(got, html.EscapeString(tt.want)) {
				t.Errorf("%v: an allowance in attention as %s does not say %q", loc, tt.category, tt.want)
			}
			if strings.Contains(got, tt.category) {
				t.Errorf("%v: an allowance in attention as %s prints the code", loc, tt.category)
			}
			sent, _, _ := strings.Cut(i18n.T(ctx, i18n.KeyAdminQueueAllowanceAwaiting), "%s")
			if strings.Contains(got, html.EscapeString(sent)) {
				t.Errorf("%v: an allowance in attention as %s says it was sent to the customer", loc, tt.category)
			}
			if strings.Contains(got, "/invoice/allowance") {
				t.Errorf("%v: a second allowance is offered while %s needs a person", loc, tt.category)
			}
		}
	}
}
