package admin

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// A pending order store credit alone paid is cancelled at once: the order page
// does not promise a refund that waits on the card and the invoice.
func TestACreditPaidCancellationSaysWhatItDoes(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		var body strings.Builder
		view := &OrderView{Number: "GO-260929-000001", Status: order.FulfillmentPending, Funded: true, RefundOffered: true, RefundCreditPaid: true}
		if err := Order(layouts.Page{Title: "order"}, view).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		page := html.UnescapeString(body.String())
		if !strings.Contains(page, i18n.T(ctx, i18n.KeyAdminRefundCreditHint)) || strings.Contains(page, i18n.T(ctx, i18n.KeyAdminRefundHint)) {
			t.Errorf("%s order page of a credit-paid order does not describe its cancellation", locale)
		}
	}
}

func TestTheOrderPageOffersTheRefundAndResume(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	render := func(v *OrderView) string {
		t.Helper()
		var body strings.Builder
		if err := Order(layouts.Page{Title: "order"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		return body.String()
	}
	action := `action="/admin/orders/GO-260929-000001/refund"`

	offered := render(&OrderView{Number: "GO-260929-000001", Status: order.FulfillmentPicking, Committed: true, RefundOffered: true})
	if !strings.Contains(offered, action) || !strings.Contains(offered, i18n.T(ctx, i18n.KeyAdminRefundHint)) {
		t.Fatal("a paid unshipped order does not offer its refund")
	}
	if strings.Contains(offered, i18n.T(ctx, i18n.KeyAdminQueueFinal)) {
		t.Fatal("a paid order in picking reads as final")
	}
	open := render(&OrderView{Number: "GO-260929-000001", Status: order.FulfillmentPicking, Committed: true, RefundOpen: true})
	if !strings.Contains(open, action) || !strings.Contains(open, i18n.T(ctx, i18n.KeyAdminRefundResume)) ||
		!strings.Contains(open, i18n.T(ctx, i18n.KeyAdminRefundOpen)) {
		t.Fatal("an open refund does not offer Resume")
	}
	if none := render(&OrderView{Number: "GO-260929-000001", Status: order.FulfillmentCancelled}); strings.Contains(none, action) ||
		!strings.Contains(none, i18n.T(ctx, i18n.KeyAdminQueueFinal)) {
		t.Fatal("a cancelled order offers a refund or does not read as final")
	}
}
