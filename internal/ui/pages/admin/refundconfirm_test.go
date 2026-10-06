package admin

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestRefundConfirmationRepeatsTheTotalItShows(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, v := range []RefundConfirmation{
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000},
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000, ReasonInvalid: true},
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000, Resume: true},
		} {
			ctx := i18n.WithLocale(t.Context(), locale)
			var body strings.Builder
			if err := ConfirmRefund(layouts.Page{Title: "refund"}, v).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			page := body.String()
			if strings.Count(page, `name="confirm"`) != 1 || !strings.Contains(page, `method="post"`) ||
				!strings.Contains(page, `name="total" value="1200000"`) || !strings.Contains(page, pages.TWD(1200000)) ||
				!strings.Contains(page, pages.TWD(900000)) || !strings.Contains(page, pages.TWD(300000)) {
				t.Fatalf("%s confirmation does not show and repeat the refund: %s", locale, page)
			}
			if strings.Contains(page, `name="reason"`) == v.Resume {
				t.Fatalf("%s resume=%t reason control disagrees", locale, v.Resume)
			}
			if strings.Contains(page, `aria-invalid="true"`) != v.ReasonInvalid ||
				strings.Contains(page, i18n.T(ctx, i18n.KeyAdminRefundErrReason)) != v.ReasonInvalid {
				t.Fatalf("%s invalid=%t reason marking disagrees", locale, v.ReasonInvalid)
			}
		}
	}
}

// A pending order store credit alone paid is cancelled at once: the
// confirmation says the credit goes back and the order page does not promise a
// refund that waits on the card and the invoice.
func TestACreditPaidCancellationSaysWhatItDoes(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		var body strings.Builder
		v := RefundConfirmation{OrderNumber: "GO-260929-000001", TotalCents: 500000, CreditCents: 500000, CreditPaid: true}
		if err := ConfirmRefund(layouts.Page{Title: "refund"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		page := html.UnescapeString(body.String())
		if !strings.Contains(page, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRefundCreditReturn), pages.TWD(500000))) ||
			!strings.Contains(page, i18n.T(ctx, i18n.KeyAdminRefundCreditCancel)) ||
			!strings.Contains(page, `name="reason"`) || !strings.Contains(page, `name="total" value="500000"`) {
			t.Errorf("%s credit-paid confirmation does not say the credit returns and the order cancels: %s", locale, page)
		}

		body.Reset()
		view := &OrderView{Number: "GO-260929-000001", Status: order.FulfillmentPending, Funded: true, RefundOffered: true, RefundCreditPaid: true}
		if err := Order(layouts.Page{Title: "order"}, view).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		page = html.UnescapeString(body.String())
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

func TestARefundBeforeShipmentIsFinishedOnItsOrderPage(t *testing.T) {
	t.Parallel()
	r := &Return{
		ID: "return", OrderNumber: "GO-260929-000001", Status: "approved", Decided: true,
		BeforeShipment: true, PayoutOutstanding: true, Window: "undelivered",
		Lines: []ReturnLine{{OrderLineID: "line", Quantity: 1}},
	}
	if r.AwaitingGoods() || r.CanComplete() {
		t.Fatal("the returns queue offers to inspect or close goods that never shipped")
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	var body strings.Builder
	if err := Returns(layouts.Page{Title: "returns"}, ReturnsView{Rows: []Return{*r}}).Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	page := body.String()
	if !strings.Contains(page, `href="/admin/orders/GO-260929-000001"`) ||
		strings.Contains(page, r.Action()) || strings.Contains(page, r.InspectAction()) {
		t.Fatalf("a refund before shipment is not sent to its order page: %s", page)
	}
}
