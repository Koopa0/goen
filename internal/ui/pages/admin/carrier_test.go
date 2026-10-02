package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheDispatchFormOffersOnlyTheClosedCarrierSet(t *testing.T) {
	t.Parallel()
	html := renderToString(t, Order(layouts.Page{Title: "GO-1"}, &OrderView{
		Number: "GO-1", CanShip: true, ShipCarrier: string(carrier.KerryTJ),
	}))

	tag := tagWithID(t, html, "ship-carrier")
	if !strings.HasPrefix(tag, "<select") {
		t.Fatalf("the carrier control is not a select: %s", tag)
	}
	for _, c := range carrier.All() {
		if !strings.Contains(html, `value="`+string(c)+`"`) {
			t.Errorf("the dispatch form does not offer %q", c)
		}
	}
	if !strings.Contains(html, `value="kerry_tj" selected`) {
		t.Error("the refused form did not keep the chosen carrier selected")
	}
}
