package i18n

import (
	"testing"

	"github.com/koopa0/goen/internal/carrier"
)

func TestEveryCarrierHasANameInBothLanguages(t *testing.T) {
	for _, loc := range Locales() {
		ctx := WithLocale(t.Context(), loc)
		home, _ := carrier.ForDelivery("", false)
		stores, _ := carrier.ForDelivery("", true)
		for _, c := range append(home, stores...) {
			if name := CarrierName(ctx, c); name == "" || name == string(c) {
				t.Errorf("%s: carrier %q has no name", loc, c)
			}
		}
	}
	if got := CarrierName(WithLocale(t.Context(), En), carrier.Carrier("terminal")); got != "terminal" {
		t.Errorf("an unknown carrier = %q, want the stored value", got)
	}
}
