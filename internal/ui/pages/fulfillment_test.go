package pages

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestEveryFulfillmentStatusHasAStorefrontLabel holds the closed set against
// the chrome: a state with no catalogue entry reaches a customer as its bare
// id, which is what StatusText only does for a retired audit value.
func TestEveryFulfillmentStatusHasAStorefrontLabel(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range FulfillmentStatuses {
			label := AccountOrder{Status: status, Committed: true}.StatusText(ctx)
			if label == "" || label == string(status) {
				t.Errorf("FulfillmentStatus(%q).StatusText in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

// TestAnUnknownFulfillmentStatusRendersAsItself holds that a retired value
// still paints: append-only history may name a state the shop no longer occupies.
func TestAnUnknownFulfillmentStatusRendersAsItself(t *testing.T) {
	t.Parallel()
	const retired FulfillmentStatus = "packing"
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := (AccountOrder{Status: retired}).StatusText(ctx); got != string(retired) {
		t.Errorf("unknown fulfilment status rendered %q, want the raw value", got)
	}
}
