package i18n

import (
	"github.com/koopa0/goen/internal/shipping"
	"testing"
)

func TestEveryCarrierHasANameInBothLanguages(t *testing.T) {
	for _, loc := range Locales() {
		ctx := WithLocale(t.Context(), loc)
		home, _ := shipping.CarriersFor("", shipping.DestinationAddress)
		stores, _ := shipping.CarriersFor("", shipping.DestinationPickupPoint)
		for _, c := range append(home, stores...) {
			if name := CarrierName(ctx, c); name == "" || name == string(c) {
				t.Errorf("%s: carrier %q has no name", loc, c)
			}
		}
	}
	if got := CarrierName(WithLocale(t.Context(), En), shipping.Carrier("terminal")); got != "terminal" {
		t.Errorf("an unknown carrier = %q, want the stored value", got)
	}
}
