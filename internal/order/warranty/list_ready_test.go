package warranty

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestList_ReadyDoesNotSendToAnOrder holds that a list with units to register
// does not tell the customer to go to an order to register: the subtitle and the
// empty state drop that direction, and each unit says which order it is on and
// when it arrived, so the same product bought twice can be told apart.
func TestList_ReadyDoesNotSendToAnOrder(t *testing.T) {
	t.Parallel()
	end := shoptime.Date{Year: 2028, Month: time.October, Day: 5, OtherYear: true}
	line := Line{ID: "band", Name: "Nimbus Band 2", HasTerm: true, Months: 24, Delivered: 1, CoverEnds: &end}
	ready := []Ready{
		{Number: "GO-260101-000001", Line: line, DeliveredOn: shoptime.Date{Year: 2026, Month: time.October, Day: 24}},
		{Number: "GO-260301-000002", Line: line, DeliveredOn: shoptime.Date{Year: 2026, Month: time.October, Day: 27}},
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		with := renderWarrantyInLocale(t, ctx, List(frame.Page{}, ListView{Ready: ready}))
		without := renderWarrantyInLocale(t, ctx, List(frame.Page{}, ListView{}))

		if !strings.Contains(without, i18n.T(ctx, i18n.KeyWarrantySub)) || !strings.Contains(without, i18n.T(ctx, i18n.KeyWarrantyNoneHint)) {
			t.Errorf("%s: a list with nothing to register lost its direction to an order", locale)
		}
		if !strings.Contains(with, i18n.T(ctx, i18n.KeyWarrantySubReady)) || strings.Contains(with, i18n.T(ctx, i18n.KeyWarrantySub)) {
			t.Errorf("%s: with units ready the subtitle still sends the customer to an order:\n%s", locale, with)
		}
		if !strings.Contains(with, i18n.T(ctx, i18n.KeyWarrantyNone)) || strings.Contains(with, i18n.T(ctx, i18n.KeyWarrantyNoneHint)) {
			t.Errorf("%s: with units ready the empty state is not just its title:\n%s", locale, with)
		}
		for _, r := range ready {
			if want := r.Meta(ctx); !strings.Contains(with, want) || !strings.Contains(want, r.Number) {
				t.Errorf("%s: the ready unit on %s does not say its order and arrival: %q", locale, r.Number, want)
			}
		}
		if a, b := ready[0].Meta(ctx), ready[1].Meta(ctx); a == b {
			t.Errorf("%s: two orders of one product read alike: %q", locale, a)
		}
	}
}
