package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/carrier"
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
