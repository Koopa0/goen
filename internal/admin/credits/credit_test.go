package credits

import (
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/credit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

var inputTag = regexp.MustCompile(`<input[^>]*>`)

// The reviewer approves what the page shows, so the checks read the text with
// every <input> removed: a value that only rides in a hidden field is not shown.
// TestCreditConfirmation: the credit confirmation shows what will be granted.
func TestCreditConfirmation(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := View{
			Confirm: true, CustomerID: "11111111-2222-3333-4444-555555555555", CustomerName: "Ada Wong", Email: "ada@example.test",
			BalanceCents: 34000, GrantCents: 12500, Amount: "125", Reason: "goodwill for a late parcel", OperationID: "op-1",
		}
		var body strings.Builder
		if err := Credit(frame.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
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

// TestCreditKindLabels: the credit ledger labels each kind and keeps staff text.
func TestCreditKindLabels(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		kind   credit.Kind
		reason string
		want   string
	}{
		{i18n.En, credit.OrderReversal, "", "Order cancelled, credit returned"},
		{i18n.En, credit.OrderSpend, "", "Applied to an order"},
		{i18n.En, credit.ReturnPayout, "", "Credit for a return"},
		{i18n.En, credit.PointsRedemption, "", "Points redeemed"},
		{i18n.ZhHant, credit.OrderSpend, "", "訂單折抵"},
		{i18n.ZhHant, credit.OrderReversal, "", "訂單取消，購物金退回"},
		{i18n.ZhHant, credit.ReturnPayout, "", "退貨退回購物金"},
		{i18n.ZhHant, credit.PointsRedemption, "", "點數兌換"},
		{i18n.En, credit.StaffGrant, "goodwill for a late parcel", "goodwill for a late parcel"},
		// Staff text that spells a label shows as typed: kind, not the words, decides.
		{i18n.En, credit.StaffGrant, "points", "points"},
		{i18n.En, credit.StaffGrant, "order cancelled", "order cancelled"},
		{i18n.ZhHant, credit.StaffGrant, "訂單折抵", "訂單折抵"},
		{i18n.ZhHant, credit.StaffGrant, "退貨退回購物金", "退貨退回購物金"},
		// And a system posting never shows its (absent or misleading) reason.
		{i18n.En, credit.PointsRedemption, "goodwill", "Points redeemed"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		var body strings.Builder
		v := View{Rows: []Entry{{Email: "a@example.test", AmountCents: -500, Kind: tt.kind, Reason: tt.reason}}}
		if err := Credit(frame.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body.String(), "<td>"+tt.want+"</td>") {
			t.Errorf("%s %s %q: ledger does not show %q", tt.locale, tt.kind, tt.reason, tt.want)
		}
	}
}

// TestCreditSourceLinks: the credit page links each row to its sources.
func TestCreditSourceLinks(t *testing.T) {
	t.Parallel()
	view := View{Rows: []Entry{
		{Email: "ada@example.test", CustomerID: "customer-a", Kind: credit.OrderSpend, OrderNumber: "GO-261009-000001", AmountCents: -10000},
		{Email: "ada@example.test", CustomerID: "customer-a", Kind: credit.ReturnPayout, OrderNumber: "GO-261009-000001", ReturnID: "return-a", AmountCents: 10000},
		{Email: "ada@example.test", CustomerID: "customer-a", Kind: credit.StaffGrant, Reason: "goodwill", AmountCents: 30000, ActorEmail: "staff@example.test"},
		{Kind: credit.StaffGrant, Reason: "erased owner", AmountCents: 100},
	}}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderComponent(t, ctx, Credit(frame.Page{Title: "credit"}, view))
		for name, want := range map[string]string{
			"customer link":   `href="/admin/customers/customer-a"`,
			"order link":      `href="/admin/orders/GO-261009-000001"`,
			"return link":     `href="/admin/returns?request=return-a"`,
			"granting staff":  "staff@example.test",
			"erased owner":    i18n.T(ctx, i18n.KeyAdminErasedShort),
			"actor column":    i18n.T(ctx, i18n.KeyAdminColActor),
			"customer column": i18n.T(ctx, i18n.KeyAdminCustColCustomer),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s credit desk lacks the %s %q", locale.Tag(), name, want)
			}
		}
		if n := strings.Count(body, `href="/admin/customers/`); n != 3 {
			t.Errorf("%s credit desk links %d customers, want 3 (the erased owner has none)", locale.Tag(), n)
		}
		if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminCreditRunningTotal)) {
			t.Errorf("%s desk for every customer shows the running total", locale.Tag())
		}
	}
}

// TestCreditRunningTotalOrder: the credit running total names its order.
func TestCreditRunningTotalOrder(t *testing.T) {
	t.Parallel()
	view := View{
		FilterCustomerID: "customer-a", FilterCustomerName: "Ada",
		Rows: []Entry{{CustomerID: "customer-a", Kind: credit.StaffGrant, Reason: "Grant", RunningTotalCents: 10000}},
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Credit(frame.Page{}, view))))
		if err != nil {
			t.Fatal(err)
		}
		var table, heading, description *html.Node
		for n := range doc.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			switch {
			case n.Data == "table":
				table = n
			case attr(n, "id") == "credit-ledger-heading":
				heading = n
			case attr(n, "id") == "credit-ledger-order":
				description = n
			}
		}
		if table == nil || heading == nil || description == nil || attr(table, "aria-labelledby") != "credit-ledger-heading" || attr(table, "aria-describedby") != "credit-ledger-order" {
			t.Errorf("%s ledger does not link its visible title and ordering explanation", locale.Tag())
			continue
		}
		if got, want := textOf(description), i18n.T(ctx, i18n.KeyAdminCreditLedgerOrder); got != want {
			t.Errorf("%s ordering explanation = %q, want %q", locale.Tag(), got, want)
		}
		label := i18n.T(ctx, i18n.KeyAdminCreditRunningTotal)
		if !hasElement(doc, func(n *html.Node) bool { return n.Data == "th" && textOf(n) == label && attr(n, "scope") == "col" }) {
			t.Errorf("%s ledger lacks a %q column header", locale.Tag(), label)
		}
		if hasElement(doc, func(n *html.Node) bool {
			return n.Data == "th" && textOf(n) == i18n.T(ctx, i18n.KeyAdminCustColCustomer)
		}) {
			t.Errorf("%s one customer's ledger repeats the customer column", locale.Tag())
		}
		body := renderComponent(t, ctx, Credit(frame.Page{}, view))
		for _, want := range []string{`<td class="goen-admin__cellnum">NT$100</td>`, `href="/admin/customers/customer-a">Ada</a>`, `href="/admin/credits">` + html.EscapeString(i18n.T(ctx, i18n.KeyAdminCreditAllCustomers))} {
			if !strings.Contains(body, want) {
				t.Errorf("%s one customer's ledger lacks %q", locale.Tag(), want)
			}
		}
	}
}

// The page passes the back-office layout a page name, and the layout compares
// it with the name on the menu link; nothing else ties the two strings, so a
// mismatch silently drops the link's current state.
func TestCreditMenuCurrent(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Credit(frame.Page{}, View{}))))
	if err != nil {
		t.Fatal(err)
	}
	if !hasElement(doc, func(n *html.Node) bool {
		return n.Data == "a" && attr(n, "href") == "/admin/credits" && attr(n, "aria-current") == "page"
	}) {
		t.Error(`the menu link to /admin/credits is not marked aria-current="page" on the credits page`)
	}
}
