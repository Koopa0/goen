package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// optionsOffered is every carrier code the dispatch form's select lists.
func optionsOffered(t *testing.T, html string) []string {
	t.Helper()
	var out []string
	for _, c := range carrier.All() {
		if strings.Contains(html, `<option value="`+string(c)+`"`) {
			out = append(out, string(c))
		}
	}
	return out
}

func TestTheDispatchFormOffersOnlyTheCarriersTheOrderCanUse(t *testing.T) {
	t.Parallel()

	t.Run("a store order lists its chain's carrier, selected", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Order(layouts.Page{Title: "GO-1"}, &OrderView{
			Number: "GO-1", CanShip: true,
			ShipCarriers: []carrier.Carrier{carrier.FamilyMart}, ShipCarrier: string(carrier.FamilyMart),
		}))
		tag := tagWithID(t, html, "ship-carrier")
		if !strings.HasPrefix(tag, "<select") {
			t.Fatalf("the carrier control is not a select: %s", tag)
		}
		if got := optionsOffered(t, html); len(got) != 1 || got[0] != "family_mart" {
			t.Errorf("a family_mart order is offered %v, want only family_mart", got)
		}
		if !strings.Contains(html, `value="family_mart" selected`) {
			t.Error("the carrier the order implies is not preselected")
		}
	})

	t.Run("a home delivery lists the home carriers and a refused choice stays selected", func(t *testing.T) {
		t.Parallel()
		home, _ := carrier.ForDelivery("")
		html := renderToString(t, Order(layouts.Page{Title: "GO-1"}, &OrderView{
			Number: "GO-1", CanShip: true, ShipCarriers: home, ShipCarrier: string(carrier.KerryTJ),
		}))
		if got := optionsOffered(t, html); len(got) != len(home) {
			t.Errorf("a home delivery is offered %v, want %d home carriers", got, len(home))
		}
		for _, store := range []carrier.Carrier{carrier.SevenEleven, carrier.FamilyMart, carrier.HiLife, carrier.OKMart} {
			if strings.Contains(html, `<option value="`+string(store)+`"`) {
				t.Errorf("a home delivery offers the convenience-store carrier %q", store)
			}
		}
		if !strings.Contains(html, `value="kerry_tj" selected`) {
			t.Error("the refused form did not keep the chosen carrier selected")
		}
	})

	t.Run("with nothing implied the placeholder is the selected option", func(t *testing.T) {
		t.Parallel()
		home, _ := carrier.ForDelivery("")
		html := renderToString(t, Order(layouts.Page{Title: "GO-1"}, &OrderView{
			Number: "GO-1", CanShip: true, ShipCarriers: home,
		}))
		if !strings.Contains(html, `<option value="" selected`) {
			t.Error("an order that implies no carrier does not ask staff to choose")
		}
	})
}
