package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestAReducedPayTotalShowsTheAdjustment holds that /pay cannot list the
// goods at their recorded prices and then name a smaller owed figure with no
// row that makes the arithmetic close. TotalCents stays "what is still owed".
func TestAReducedPayTotalShowsTheAdjustment(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 56000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
		Enabled: true,
	}
	if view.AdjustmentCents() != 44000 {
		t.Fatalf("AdjustmentCents() = %d, want 44000", view.AdjustmentCents())
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	applied := i18n.T(ctx, i18n.KeyPayApplied)
	if !strings.Contains(html, applied) {
		t.Errorf("a pay page that owes less than its lines does not show %q", applied)
	}
	if !strings.Contains(html, "-NT$440") {
		t.Errorf("a pay page that owes NT$560 against NT$1,000 of lines does not show -NT$440")
	}
	if !strings.Contains(html, "NT$560") {
		t.Errorf("the owed total is missing from the pay page")
	}

	plain := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 100000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
	}
	if plain.HasAdjustment() {
		t.Fatal("a pay page whose lines already equal the owed total grew an adjustment")
	}
	plainHTML := renderToString(t, Pay(layouts.Page{Title: "Pay"}, plain))
	if strings.Contains(plainHTML, applied) {
		t.Errorf("an unreduced pay page still shows %q", applied)
	}
}

// TestAPayTotalWithShippingAndCreditShowsTheNetReduction holds that a mixed
// order still names the net, not the credit half. Lines NT$1,000 plus
// delivery and tax NT$100 minus credit NT$200 owes NT$900: the row is
// 已折抵 −NT$100, never the NT$200 that was credited.
func TestAPayTotalWithShippingAndCreditShowsTheNetReduction(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 90000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
	}
	if view.AdjustmentCents() != 10000 {
		t.Fatalf("AdjustmentCents() = %d, want 10000", view.AdjustmentCents())
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	applied := i18n.T(ctx, i18n.KeyPayApplied)
	if !strings.Contains(html, applied) {
		t.Errorf("a pay page whose net is a reduction does not show %q", applied)
	}
	if !strings.Contains(html, "-NT$100") {
		t.Errorf("a mixed shipping-plus-credit pay page does not show the net -NT$100")
	}
	if strings.Contains(html, "NT$200") {
		t.Error("a mixed pay page named the credit half instead of the net reduction")
	}
	if strings.Contains(html, i18n.T(ctx, i18n.KeyShippingAndTax)) {
		t.Error("a net-reduction pay page labelled the row as delivery and tax")
	}
}

// TestAPayTotalAboveTheLinesHasNoAdjustmentRow holds that owed above the
// line sum is not a labelled shipping row. Lines NT$1,000 plus delivery
// and tax NT$100 minus credit NT$50 owes NT$1,050: the view has no
// breakdown that could name that NT$50 truthfully.
func TestAPayTotalAboveTheLinesHasNoAdjustmentRow(t *testing.T) {
	t.Parallel()

	view := PayView{
		Number:     "GOEN-PAY",
		TotalCents: 105000,
		Lines: []PayLine{
			{Name: "Nimbus", UnitCents: 100000, Quantity: 1},
		},
	}
	if view.HasAdjustment() {
		t.Fatal("a pay page that owes more than its lines grew an adjustment")
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Pay(layouts.Page{Title: "Pay"}, view))
	if strings.Contains(html, i18n.T(ctx, i18n.KeyPayApplied)) {
		t.Error("a pay page that owes more than its lines used the reduction label")
	}
	if strings.Contains(html, i18n.T(ctx, i18n.KeyShippingAndTax)) {
		t.Error("a pay page that owes more than its lines invented a delivery-and-tax row")
	}
	if !strings.Contains(html, "NT$1,050") {
		t.Errorf("the owed total is missing from the pay page")
	}
}
