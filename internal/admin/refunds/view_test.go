package refunds

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestConfirmRefundRepeatsTotal: the refund confirmation repeats the total it
// shows.
func TestConfirmRefundRepeatsTotal(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, v := range []RefundConfirmation{
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000},
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000, ReasonInvalid: true},
			{OrderNumber: "GO-260929-000001", TotalCents: 1200000, CardCents: 900000, CreditCents: 300000, Resume: true},
		} {
			ctx := i18n.WithLocale(t.Context(), locale)
			var body strings.Builder
			if err := ConfirmRefund(frame.Page{Title: "refund"}, v).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			page := body.String()
			if strings.Count(page, `name="confirm"`) != 1 || !strings.Contains(page, `method="post"`) ||
				!strings.Contains(page, `name="total" value="1200000"`) || !strings.Contains(page, money.TWD(1200000)) ||
				!strings.Contains(page, money.TWD(900000)) || !strings.Contains(page, money.TWD(300000)) {
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

// TestConfirmRefundCreditPaid: a pending order store credit alone paid is
// cancelled at once: the confirmation says the credit goes back.
func TestConfirmRefundCreditPaid(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		var body strings.Builder
		v := RefundConfirmation{OrderNumber: "GO-260929-000001", TotalCents: 500000, CreditCents: 500000, CreditPaid: true}
		if err := ConfirmRefund(frame.Page{Title: "refund"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		page := html.UnescapeString(body.String())
		if !strings.Contains(page, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRefundCreditReturn), money.TWD(500000))) ||
			!strings.Contains(page, i18n.T(ctx, i18n.KeyAdminRefundCreditCancel)) ||
			!strings.Contains(page, `name="reason"`) || !strings.Contains(page, `name="total" value="500000"`) {
			t.Errorf("%s credit-paid confirmation does not say the credit returns and the order cancels: %s", locale, page)
		}
		if strings.Contains(page, v.Channel(ctx)) {
			t.Errorf("%s credit-paid confirmation repeats the payout channel %q beside the cancellation", locale, v.Channel(ctx))
		}
	}
}

// TestConfirmRefundCancellation: the cancellation's confirm page is about a
// cancellation: its figure is what is refunded for cancelling, and the one field
// it insists on says so.
func TestConfirmRefundCancellation(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		markup := renderComponent(t, ctx, ConfirmRefund(frame.Page{Title: "refund"},
			RefundConfirmation{OrderNumber: "GO-1", TotalCents: 100000}))
		if !strings.Contains(markup, i18n.T(ctx, i18n.KeyAdminRefundConfirmAmount)) {
			t.Errorf("%s: the amount is not labelled as a cancellation refund", locale)
		}
		if strings.Contains(markup, i18n.T(ctx, i18n.KeyAdminRetConfirmAmount)) {
			t.Errorf("%s: a cancellation is labelled with the return amount", locale)
		}
		_, label, _ := strings.Cut(markup, `for="refund-reason"`)
		label, _, _ = strings.Cut(label, "</label>")
		if !strings.Contains(label, i18n.T(ctx, i18n.KeyAdminRequiredMark)) {
			t.Errorf("%s: the required note is not marked required: %s", locale, label)
		}

		resumed := renderComponent(t, ctx, ConfirmRefund(frame.Page{Title: "refund"},
			RefundConfirmation{OrderNumber: "GO-1", TotalCents: 100000, Resume: true}))
		if strings.Contains(resumed, i18n.T(ctx, i18n.KeyAdminRequiredMark)) {
			t.Errorf("%s: a resumed refund, which has no note field, marks one required", locale)
		}
	}
}
