package i18n

import (
	"testing"

	"github.com/koopa0/goen/internal/carrier"
)

func TestEveryCarrierHasANameInBothLanguages(t *testing.T) {
	for _, loc := range Locales() {
		ctx := WithLocale(t.Context(), loc)
		for _, c := range carrier.All() {
			if name := CarrierName(ctx, c); name == "" || name == string(c) {
				t.Errorf("%s: carrier %q has no name", loc, c)
			}
		}
	}
	if got := CarrierName(WithLocale(t.Context(), En), carrier.Carrier("terminal")); got != "terminal" {
		t.Errorf("an unknown carrier = %q, want the stored value", got)
	}
}
