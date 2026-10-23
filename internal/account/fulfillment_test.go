package account

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestOrder_StatusTextLabels holds the closed set against the chrome: a
// state with no catalogue entry reaches a customer as its bare id, which is
// what StatusText only does for a retired audit value.
func TestOrder_StatusTextLabels(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range order.FulfillmentStatuses {
			label := Order{Status: status, Committed: true}.StatusText(ctx)
			if label == "" || label == string(status) {
				t.Errorf("FulfillmentStatus(%q).StatusText in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

// TestOrder_StatusTextUnknown holds that a retired value still paints:
// append-only history may name a state the shop no longer occupies.
func TestOrder_StatusTextUnknown(t *testing.T) {
	t.Parallel()
	const retired order.FulfillmentStatus = "packing"
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := (Order{Status: retired}).StatusText(ctx); got != string(retired) {
		t.Errorf("unknown fulfilment status rendered %q, want the raw value", got)
	}
}
