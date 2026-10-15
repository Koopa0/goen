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

func TestCreditLedgerLabelsEachKindAndKeepsStaffText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		kind   PostingKind
		reason string
		want   string
	}{
		{i18n.En, PostingOrderReversal, "", "Order cancelled, credit returned"},
		{i18n.En, PostingOrderSpend, "", "Applied to an order"},
		{i18n.En, PostingReturnPayout, "", "Credit for a return"},
		{i18n.En, PostingPointsRedemption, "", "Points redeemed"},
		{i18n.ZhHant, PostingOrderSpend, "", "訂單折抵"},
		{i18n.ZhHant, PostingOrderReversal, "", "訂單取消，購物金退回"},
		{i18n.ZhHant, PostingReturnPayout, "", "退貨退回購物金"},
		{i18n.ZhHant, PostingPointsRedemption, "", "點數兌換"},
		{i18n.En, PostingStaffGrant, "goodwill for a late parcel", "goodwill for a late parcel"},
		// Staff text that spells a label shows as typed: kind, not the words, decides.
		{i18n.En, PostingStaffGrant, "points", "points"},
		{i18n.En, PostingStaffGrant, "order cancelled", "order cancelled"},
		{i18n.ZhHant, PostingStaffGrant, "訂單折抵", "訂單折抵"},
		{i18n.ZhHant, PostingStaffGrant, "退貨退回購物金", "退貨退回購物金"},
		// And a system posting never shows its (absent or misleading) reason.
		{i18n.En, PostingPointsRedemption, "goodwill", "Points redeemed"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		var body strings.Builder
		v := CreditView{Rows: []CreditEntry{{Email: "a@example.test", AmountCents: -500, Kind: tt.kind, Reason: tt.reason}}}
		if err := Credit(layouts.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.String(), ">"+tt.want+"</span>") {
			t.Errorf("%s %s %q: ledger does not show %q", tt.locale, tt.kind, tt.reason, tt.want)
		}
	}
}
