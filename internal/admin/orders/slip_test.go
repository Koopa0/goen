package orders

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// slipOrder sets every field a packing slip must not carry beside every one it
// must, so a rendering shows which of them it kept.
func slipOrder(outstanding bool) *OrderView {
	return &OrderView{
		OutstandingQuantities: outstanding,
		Number:                "GO-SLIP",
		Recipient:             "林收件", Address: "臺北市中正區仁愛路 1 號",
		Email: "buyer@example.com", Phone: "0912345678",
		InvoiceType: invoice.PreferenceMobile, InvoiceMobileBarcode: "/ABC1234",
		Lines: []Line{{SKU: "SKU-SLIP", Name: "Slip product", Label: "Blue", UnitCents: 345600, Quantity: 3}},
	}
}

// slipCarries is what the packer and the recipient need: the recipient and the
// address, and each line's product, option and quantity.
func slipCarries(line Line, v *OrderView) []string {
	return []string{v.Recipient, v.Address, line.SKU, line.Name, line.Label, "× " + line.QuantityText()}
}

// slipDrops is what stays out of a parcel: the email and phone, the invoice
// details and the prices.
func slipDrops(line Line, v *OrderView) []string {
	return []string{v.Email, v.Phone, v.InvoiceMobileBarcode, line.UnitPrice(), line.LineTotal()}
}

// TestOrderItemsAndDeliveryBatch: a batch slip prints the recipient, the
// address and the lines' product, option and quantity, and none of what the
// parcel must not carry, even from a view that holds it.
func TestOrderItemsAndDeliveryBatch(t *testing.T) {
	t.Parallel()
	v := slipOrder(true)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		out := renderComponent(t, i18n.WithLocale(t.Context(), locale), orderItemsAndDelivery(v))
		for _, want := range slipCarries(v.Lines[0], v) {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the batch slip lost %q", locale, want)
			}
		}
		for _, unwanted := range slipDrops(v.Lines[0], v) {
			if unwanted == "" || strings.Contains(out, unwanted) {
				t.Errorf("%s: the batch slip carries %q", locale, unwanted)
			}
		}
		doc, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := slipLine(t, doc, v.Lines[0], true), "SKU-SLIP × 3"; got != want {
			t.Errorf("%s: the batch slip's line reads %q, want %q", locale, got, want)
		}
	}
}

// TestOrderItemsAndDeliveryPrinted: the order page keeps its prices, email,
// phone and invoice on the screen for staff, and marks each for the print
// stylesheet, so the page printed as the single slip drops them; what the
// parcel needs carries no mark.
func TestOrderItemsAndDeliveryPrinted(t *testing.T) {
	t.Parallel()
	v := slipOrder(false)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		out := renderComponent(t, i18n.WithLocale(t.Context(), locale), orderItemsAndDelivery(v))
		doc, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range slipDrops(v.Lines[0], v) {
			if shown, marked := offSlipCounts(doc, text); text == "" || shown == 0 || marked != shown {
				t.Errorf("%s: %q shows %d times, %d of them marked off the slip, want every one", locale, text, shown, marked)
			}
		}
		for _, text := range slipCarries(v.Lines[0], v) {
			if shown, marked := offSlipCounts(doc, text); shown == 0 || marked != 0 {
				t.Errorf("%s: %q shows %d times, %d of them marked off the slip, want none", locale, text, shown, marked)
			}
		}
		if got, want := slipLine(t, doc, v.Lines[0], true), "SKU-SLIP × 3"; got != want {
			t.Errorf("%s: the printed line reads %q, want %q", locale, got, want)
		}
		if got, want := slipLine(t, doc, v.Lines[0], false), "SKU-SLIP · NT$3,456 × 3"; got != want {
			t.Errorf("%s: the line on screen reads %q, want %q", locale, got, want)
		}
	}
}

// slipLine is the text of the meta line that holds the line's SKU, its runs of
// white space folded to one space. With onSlip it leaves out what the print
// stylesheet hides, so it reads as the parcel's slip does.
func slipLine(t *testing.T, doc *html.Node, line Line, onSlip bool) string {
	t.Helper()
	metas := findAll(doc, func(n *html.Node) bool {
		return slices.Contains(strings.Fields(attr(n, "class")), "goen-admin__meta") && strings.Contains(textOf(n), line.SKU)
	})
	if len(metas) != 1 {
		t.Fatalf("%d meta lines hold %q, want 1", len(metas), line.SKU)
	}
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		if onSlip && n.Type == html.ElementNode && slices.Contains(strings.Fields(attr(n, "class")), "goen-admin__offslip") {
			return ""
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(text(c))
		}
		return b.String()
	}
	return strings.Join(strings.Fields(text(metas[0])), " ")
}

// offSlipCounts counts the text nodes of doc that hold text and how many of
// those sit inside an element marked goen-admin__offslip.
func offSlipCounts(doc *html.Node, text string) (shown, marked int) {
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inside bool) {
		if n.Type == html.ElementNode && slices.Contains(strings.Fields(attr(n, "class")), "goen-admin__offslip") {
			inside = true
		}
		if n.Type == html.TextNode && strings.Contains(n.Data, text) {
			shown++
			if inside {
				marked++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
	}
	walk(doc, false)
	return shown, marked
}
