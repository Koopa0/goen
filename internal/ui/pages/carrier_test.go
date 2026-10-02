package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheOrderPageLinksEachParcelToItsCarrier(t *testing.T) {
	t.Parallel()
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, &OrderView{
		Number: "GO-1",
		Shipments: []OrderShipment{
			{Carrier: carrier.BlackCat, Tracking: "903221488720", ShippedAt: "10/01 09:00"},
			{Carrier: carrier.HCT, Tracking: "1234567890", ShippedAt: "10/01 09:00"},
			{Carrier: carrier.OKMart, Tracking: "OK123", ShippedAt: "10/01 09:00"},
		},
	}))

	for _, want := range []string{
		"黑貓宅急便", "新竹物流", "OK 超商",
		// A carrier whose address takes the number links the number itself.
		`href="https://www.t-cat.com.tw/Inquire/TraceDetail.aspx?BillID=903221488720"`,
		// One that does not links its lookup page beside the number as text.
		`href="https://www.hct.com.tw/Search/SearchGoods_n.aspx"`,
		"查詢編號 1234567890",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("order page does not contain %q", want)
		}
	}
	if strings.Contains(html, "okmart") {
		t.Error("order page links a page for a carrier with no confirmed tracking page")
	}
	if strings.Count(html, "到物流商網站查詢") != 1 {
		t.Errorf("want one lookup link, for HCT only; got %d", strings.Count(html, "到物流商網站查詢"))
	}
}

// A closed set — the carriers, the history's statuses, the page's own chrome —
// must read in English on an English page. The shop's own entries (a product
// name, a typed address) are data and are left out of the scan.
func TestTheEnglishOrderPageHasNoChineseOfItsOwn(t *testing.T) {
	t.Parallel()

	shipments := make([]OrderShipment, 0, len(carrier.All()))
	for _, c := range carrier.All() {
		shipments = append(shipments, OrderShipment{Carrier: c, Tracking: "T1", ShippedAt: "10/01 09:00"})
	}
	kinds := []string{"placed", "paid", "picking", "shipped", "in_transit", "delivered", "completed"}
	timeline := make([]OrderEvent, 0, len(kinds))
	for _, kind := range kinds {
		timeline = append(timeline, OrderEvent{Kind: kind, At: "10/01 09:00"})
	}
	const userData = "台北市信義區松高路 68 號"
	v := &OrderView{
		Number:       "GO-1",
		Status:       FulfillmentShipped,
		ShippingName: "Home delivery",
		DeliveryTo:   userData,
		Lines:        []OrderLine{{SKU: "S1", Name: "Aurora 充電器", Label: "銀", UnitCents: 1000, Quantity: 1}},
		Timeline:     timeline,
		Shipments:    shipments,
	}
	out := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Order(layouts.Page{Title: "Order"}, v))

	// 繁體中文 is the language switch naming Chinese in Chinese, on purpose.
	for _, data := range []string{userData, "Aurora 充電器", "銀", "繁體中文"} {
		out = strings.ReplaceAll(out, data, "")
	}
	// Tag and attribute text is markup, not copy; only what a reader sees counts.
	out = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<[^>]*>`).ReplaceAllString(out, " ")
	if han := regexp.MustCompile(`\p{Han}+`).FindAllString(out, -1); len(han) > 0 {
		t.Errorf("the English order page shows Chinese of its own: %q", han)
	}
}
