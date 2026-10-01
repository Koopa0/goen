package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// The shipping strip may name store pickup only where checkout offers it.
func TestTheShippingStripNamesPickupOnlyWhereItIsOffered(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		with := (&HomeView{PickupOffered: true}).ShippingBodyKey()
		without := (&HomeView{}).ShippingBodyKey()
		if with == without {
			t.Fatalf("%s: the strip says the same thing with and without pickup", locale)
		}
		pickup := map[i18n.Locale]string{i18n.ZhHant: "超商取貨", i18n.En: "pickup"}[locale]
		if !strings.Contains(i18n.T(ctx, with), pickup) {
			t.Errorf("%s: the pickup sentence does not name pickup", locale)
		}
		if strings.Contains(i18n.T(ctx, without), pickup) {
			t.Errorf("%s: the sentence for a shop without pickup names pickup", locale)
		}
	}
}
