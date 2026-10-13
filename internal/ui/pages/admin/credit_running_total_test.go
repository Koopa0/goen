package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCreditLedgerNamesAndExplainsItsAccountingOrder(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderComponent(t, ctx, Credit(layouts.Page{}, CreditView{Rows: []CreditEntry{{Reason: "Grant", RunningTotalCents: 10000}}}))
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var table, heading, description *html.Node
		for n := range doc.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			if n.Data == "table" {
				table = n
			}
			if attr(n, "id") == "credit-ledger-heading" {
				heading = n
			}
			if attr(n, "id") == "credit-ledger-order" {
				description = n
			}
		}
		label := "Ledger running total"
		explanation := "Totals follow each customer's ledger in recorded-time order, with entry IDs breaking ties. This order does not represent transaction completion order."
		if locale == i18n.ZhHant {
			label = "帳列累計"
			explanation = "依每位顧客的異動記錄時間累加；同時刻以識別碼排序。這個順序不代表交易完成順序。"
		}
		if !hasElement(doc, func(n *html.Node) bool { return n.Data == "th" && textOf(n) == label && attr(n, "scope") == "col" }) {
			t.Errorf("%s ledger column lacks truthful accounting wording", locale)
		}
		if table == nil || heading == nil || description == nil || attr(table, "aria-labelledby") != "credit-ledger-heading" || attr(table, "aria-describedby") != "credit-ledger-order" {
			t.Errorf("%s ledger does not link its visible title and ordering explanation", locale)
			continue
		}
		if textOf(description) != explanation {
			t.Errorf("%s ordering explanation does not state the accounting order", locale)
		}
	}
}
