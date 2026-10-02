package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func renderOrder(t *testing.T, locale i18n.Locale, v *OrderView) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), locale)
	return renderComponent(t, ctx, Order(layouts.Page{Title: "order"}, v))
}

// A paid order awaiting fulfilment has one move, so it is one button: a menu of
// one entry and a second button that reads "update" make staff work out which
// of the two does the thing.
func TestAPaidOrderAwaitingFulfilmentStartsPickingWithOneButton(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := &OrderView{
			Number: "GO-1", Status: pages.FulfillmentPending, Committed: true,
			Next: []Transition{{Value: pages.FulfillmentPicking, Label: "picking"}},
		}
		html := renderOrder(t, locale, v)
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueStartPicking)) {
			t.Errorf("%s: no start-picking button", locale)
		}
		if !strings.Contains(html, `type="hidden" name="status" value="picking"`) {
			t.Errorf("%s: the button does not post the picking status", locale)
		}
		if strings.Contains(html, `id="next-status"`) {
			t.Errorf("%s: the one-entry status menu is still there", locale)
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueStatusSave)) {
			t.Errorf("%s: the generic update button is still there", locale)
		}
	}

	t.Run("other moves keep the menu", func(t *testing.T) {
		t.Parallel()
		for name, next := range map[string][]Transition{
			"cancel only":    {{Value: pages.FulfillmentCancelled}},
			"two moves":      {{Value: pages.FulfillmentPicking}, {Value: pages.FulfillmentCancelled}},
			"deliver or end": {{Value: pages.FulfillmentDelivered}, {Value: pages.FulfillmentCompleted}},
		} {
			html := renderOrder(t, i18n.En, &OrderView{Number: "GO-1", Status: pages.FulfillmentPending, Next: next})
			if !strings.Contains(html, `id="next-status"`) {
				t.Errorf("%s: lost its status menu", name)
			}
		}
	})
}

// The refund before shipment is offered from the paid state, not only from
// picking: the database admits 'pending' and 'picking' alike.
func TestAPaidOrderAwaitingFulfilmentOffersTheRefundBeforeShipment(t *testing.T) {
	t.Parallel()
	html := renderOrder(t, i18n.En, &OrderView{
		Number: "GO-1", Status: pages.FulfillmentPending, Committed: true, RefundOffered: true,
		Next: []Transition{{Value: pages.FulfillmentPicking}},
	})
	if !strings.Contains(html, `action="/admin/orders/GO-1/refund"`) {
		t.Fatal("a paid order awaiting fulfilment has no refund before shipment")
	}
}

// The cancellation's confirm page is about a cancellation: its figure is what is
// refunded for cancelling, and the one field it insists on says so.
func TestTheCancellationConfirmSaysCancellationAndMarksTheRequiredNote(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		html := renderComponent(t, ctx, ConfirmRefund(layouts.Page{Title: "refund"},
			RefundConfirmation{OrderNumber: "GO-1", TotalCents: 100000}))
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRefundConfirmAmount)) {
			t.Errorf("%s: the amount is not labelled as a cancellation refund", locale)
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRetConfirmAmount)) {
			t.Errorf("%s: a cancellation is labelled with the return amount", locale)
		}
		_, label, _ := strings.Cut(html, `for="refund-reason"`)
		label, _, _ = strings.Cut(label, "</label>")
		if !strings.Contains(label, i18n.T(ctx, i18n.KeyAdminRequiredMark)) {
			t.Errorf("%s: the required note is not marked required: %s", locale, label)
		}

		resumed := renderComponent(t, ctx, ConfirmRefund(layouts.Page{Title: "refund"},
			RefundConfirmation{OrderNumber: "GO-1", TotalCents: 100000, Resume: true}))
		if strings.Contains(resumed, i18n.T(ctx, i18n.KeyAdminRequiredMark)) {
			t.Errorf("%s: a resumed refund, which has no note field, marks one required", locale)
		}
	}
}

// The return decision marks its note only when the decision needs one.
func TestTheReturnDecisionMarksTheNoteOnlyWhenItIsRequired(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, required := range []bool{true, false} {
		html := renderComponent(t, ctx, ConfirmReturn(layouts.Page{Title: "r"},
			ReturnConfirmation{ID: "id", OrderNumber: "GO-1", Decision: "rejected", Required: required}))
		if got := strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRequiredMark)); got != required {
			t.Errorf("required=%t but the mark is shown=%t", required, got)
		}
	}
}

// Staff cannot change the server's environment, so the panel says only that
// e-invoicing is not on.
func TestTheInvoicePanelNamesNoSettingToStaff(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		html := renderOrder(t, locale, &OrderView{Number: "GO-1", Status: pages.FulfillmentPending})
		ctx := i18n.WithLocale(t.Context(), locale)
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueNoInvoicing)) {
			t.Fatalf("%s: the panel does not say invoicing is off", locale)
		}
		if strings.Contains(html, "GOEN_") {
			t.Errorf("%s: the order page shows staff an environment variable", locale)
		}
	}
}

// A cancelled order is not going anywhere, so it offers no form to change where.
func TestACancelledOrderOffersNoDeliveryCorrection(t *testing.T) {
	t.Parallel()
	for _, correctable := range []bool{true, false} {
		html := renderOrder(t, i18n.En, &OrderView{Number: "GO-1", Status: pages.FulfillmentCancelled, Correctable: correctable})
		if got := strings.Contains(html, `action="/admin/orders/GO-1/delivery"`); got != correctable {
			t.Errorf("Correctable=%t but the delivery form is shown=%t", correctable, got)
		}
	}
}
