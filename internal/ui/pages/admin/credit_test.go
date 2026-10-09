package admin

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

var inputTag = regexp.MustCompile(`<input[^>]*>`)

// The reviewer approves what the page shows, so the checks read the text with
// every <input> removed: a value that only rides in a hidden field is not shown.
func TestCreditConfirmationShowsWhatWillBeGranted(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := CreditView{
			Confirm: true, CustomerID: "11111111-2222-3333-4444-555555555555", CustomerName: "Ada Wong", Email: "ada@example.test",
			BalanceCents: 34000, GrantCents: 12500, Amount: "125", Reason: "goodwill for a late parcel", OperationID: "op-1",
		}
		var body strings.Builder
		if err := Credit(layouts.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		visible := inputTag.ReplaceAllString(body.String(), "")
		for name, want := range map[string]string{
			"grant amount": "NT$125", "current balance": "NT$340", "customer name": "Ada Wong",
			"customer email": "ada@example.test", "reason": "goodwill for a late parcel",
		} {
			if !strings.Contains(visible, want) {
				t.Errorf("%s: confirmation does not show %s %q", locale, name, want)
			}
		}
	}
}

func TestCreditLedgerTranslatesTheReasonsGoenWroteAndKeepsStaffText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		reason string
		want   string
	}{
		{i18n.En, "order cancelled", "Order cancelled, credit returned"},
		{i18n.En, "訂單折抵", "Applied to an order"},
		{i18n.En, "退貨退回購物金", "Credit for a return"},
		{i18n.En, "points", "Points redeemed"},
		{i18n.ZhHant, "order cancelled", "訂單取消，購物金退回"},
		{i18n.ZhHant, "points", "點數兌換"},
		{i18n.En, "goodwill for a late parcel", "goodwill for a late parcel"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		var body strings.Builder
		v := CreditView{Rows: []CreditEntry{{Email: "a@example.test", AmountCents: -500, Reason: tt.reason}}}
		if err := Credit(layouts.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.String(), ">"+tt.want+"</span>") {
			t.Errorf("%s %q: ledger does not show %q", tt.locale, tt.reason, tt.want)
		}
	}
}

func TestCreditLedgerLinksItsCustomerAndSourceAndShowsBalanceAndActor(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := CreditView{Rows: []CreditEntry{
			{Email: "ada@example.test", CustomerID: "customer-a", OrderNumber: "GO-261009-000001", Reason: reasonOrderSpend, AmountCents: -10000, BalanceCents: 20000},
			{Email: "ada@example.test", CustomerID: "customer-a", OrderNumber: "GO-261009-000001", ReturnID: "return-a", Reason: reasonReturnPayout, AmountCents: 10000, BalanceCents: 30000},
			{Email: "ada@example.test", CustomerID: "customer-a", Reason: "goodwill", AmountCents: 30000, BalanceCents: 30000, ActorName: "Staff Ada"},
		}}
		var body strings.Builder
		if err := Credit(layouts.Page{Title: "credit"}, view).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`href="/admin/customers/customer-a"`, `href="/admin/orders/GO-261009-000001"`, `href="/admin/returns?request=return-a"`, "NT$200", "NT$300", "Staff Ada"} {
			if !strings.Contains(body.String(), want) {
				t.Errorf("%s credit ledger lacks %q", locale.Tag(), want)
			}
		}
	}
}
