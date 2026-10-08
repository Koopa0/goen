package admin

import (
	"html"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAllowanceNoticesRemainVisibleWithoutInvoicingActions(t *testing.T) {
	t.Parallel()
	for _, language := range []struct {
		locale      i18n.Locale
		disabled    string
		awaiting    string
		unconfirmed string
		held        string
		mismatch    string
	}{
		{
			locale:      i18n.ZhHant,
			disabled:    "尚未啟用電子發票，這裡無法開立、作廢或折讓。",
			awaiting:    "已寄出折讓確認信，等待顧客在 2026-10-10 15:00 前確認。",
			unconfirmed: "顧客未在 72 小時內確認折讓。",
			held:        "綠界表示這張發票可折讓的金額仍被先前未確認的折讓保留，這次沒有開立任何折讓。",
			mismatch:    "綠界的回覆指向另一張發票，無法確認這筆折讓是否已寄出給顧客。",
		},
		{
			locale:      i18n.En,
			disabled:    "E-invoicing is not set up for this shop, so invoices cannot be issued, voided or credited from here.",
			awaiting:    "The credit note was e-mailed to the customer to agree to by 2026-10-10 15:00.",
			unconfirmed: "The customer did not agree to the credit note within 72 hours.",
			held:        "ECPay says an earlier credit note the customer never agreed to still holds this invoice's amount, so nothing was filed this time.",
			mismatch:    "ECPay's reply named another invoice, so whether this credit note reached the customer is unknown.",
		},
	} {
		for _, provider := range []struct {
			name    string
			enabled bool
		}{
			{name: "disabled"},
			{name: "enabled", enabled: true},
		} {
			for _, tt := range []struct {
				name           string
				awaiting       string
				attention      string
				message        string
				noInvoice      bool
				enabledActions []string
			}{
				{name: "pending", awaiting: "2026-10-10 15:00", message: language.awaiting, enabledActions: []string{"void"}},
				{name: "unconfirmed", attention: invoice.CategoryBuyerUnconfirmed, message: language.unconfirmed, enabledActions: []string{"void"}},
				{name: "held amount", attention: invoice.CategoryAmountStillHeld, message: language.held, enabledActions: []string{"void"}},
				{name: "provider mismatch", attention: invoice.CategorySuccessMismatch, message: language.mismatch, enabledActions: []string{"void"}},
				{name: "no open allowance", enabledActions: []string{"void", "allowance"}},
				{name: "no invoice", noInvoice: true, enabledActions: []string{"issue"}},
			} {
				t.Run(language.locale.Tag()+"/"+provider.name+"/"+tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), language.locale)
					view := OrderView{
						Number: "GO-ALLOWANCE", Status: order.FulfillmentCompleted,
						Committed: true, SubtotalCents: 100000, InvoiceType: invoice.PreferenceMember,
						InvoicingEnabled: provider.enabled, RefundedCents: 84900,
						AllowanceOperationID:   "new-credit-note",
						AllowanceAwaitingUntil: tt.awaiting, AllowanceAttention: tt.attention,
					}
					if !tt.noInvoice {
						view.InvoiceDocuments = []InvoiceDocument{
							{Kind: invoice.DocumentInvoice, Number: "LC97535645", Status: invoice.DocumentIssued, AmountCents: 100000},
							{Kind: invoice.DocumentAllowance, Number: "CN-PREVIOUS", Status: invoice.DocumentIssued, AmountCents: 20000},
						}
					}
					body := renderComponent(t, ctx, Order(layouts.Page{Title: "GO-ALLOWANCE"}, &view))
					wantCounts := map[string]int{
						language.awaiting: 0, language.unconfirmed: 0, language.held: 0, language.mismatch: 0,
					}
					if tt.message != "" {
						wantCounts[tt.message] = 1
					}
					counts := make(map[string]int, len(wantCounts))
					for message := range wantCounts {
						counts[message] = strings.Count(body, html.EscapeString(message))
					}
					if diff := cmp.Diff(wantCounts, counts); diff != "" {
						t.Errorf("Order() allowance notices (-want +got):\n%s", diff)
					}
					if tt.attention != "" && !strings.Contains(body, `<p class="ui-alert ui-alert--error" role="status">`+html.EscapeString(tt.message)+`</p>`) {
						t.Error("Order() allowance attention has no visible status region")
					}
					for _, number := range []string{"LC97535645", "CN-PREVIOUS"} {
						want := 1
						if tt.noInvoice {
							want = 0
						}
						if got := strings.Count(body, number); got != want {
							t.Errorf("Order() document %q count = %d, want %d", number, got, want)
						}
					}
					wantDisabled := 1
					wantActions := []string{}
					if provider.enabled {
						wantDisabled = 0
						wantActions = tt.enabledActions
					}
					if got := strings.Count(body, html.EscapeString(language.disabled)); got != wantDisabled {
						t.Errorf("Order() unavailable-actions notice count = %d, want %d", got, wantDisabled)
					}
					actions := []string{}
					for _, action := range []struct{ name, path string }{
						{name: "issue", path: "/invoice"},
						{name: "void", path: "/invoice/void"},
						{name: "allowance", path: "/invoice/allowance"},
					} {
						if strings.Contains(body, `action="/admin/orders/GO-ALLOWANCE`+action.path+`"`) {
							actions = append(actions, action.name)
						}
					}
					if diff := cmp.Diff(wantActions, actions); diff != "" {
						t.Errorf("Order() invoice actions (-want +got):\n%s", diff)
					}
				})
			}
		}
	}
}
