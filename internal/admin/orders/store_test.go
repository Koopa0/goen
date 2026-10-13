package orders

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestRefusedIfNoRowRefusesOnlyAMissingRow(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		err     error
		refused bool
	}{
		{name: "no row", err: pgx.ErrNoRows, refused: true},
		{name: "lock timeout", err: &pgconn.PgError{Code: "55P03"}, refused: false},
	} {
		got := refusedIfNoRow(tt.err, "read the row")
		if errors.Is(got, ErrRefused) != tt.refused {
			t.Errorf("refusedIfNoRow(%s) = %v, refused %t, want %t", tt.name, got, !tt.refused, tt.refused)
		}
		if !errors.Is(got, tt.err) {
			t.Errorf("refusedIfNoRow(%s) = %v, which no longer wraps %v", tt.name, got, tt.err)
		}
	}
}

// TestAnOrderRefundedInFullSaysSoInPlaceOfItsDelivery holds orderStatus without the database: an order the
// settled-refund query names says it was refunded, and any other order keeps its delivery word.
func TestAnOrderRefundedInFullSaysSoInPlaceOfItsDelivery(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	text, intent := orderStatus(ctx, order.FulfillmentDelivered, true, 0, true)
	if want := i18n.T(ctx, i18n.KeyStatusRefunded); text != want || intent != components.IntentNeutral {
		t.Errorf("orderStatus(delivered, returned) = %q, %q; want %q, %q", text, intent, want, components.IntentNeutral)
	}
	text, _ = orderStatus(ctx, order.FulfillmentDelivered, true, 0, false)
	if want := admin.FundedFulfillmentLabel(ctx, order.FulfillmentDelivered, true, 0); text != want {
		t.Errorf("orderStatus(delivered, not returned) = %q, want %q", text, want)
	}
}

func TestOrderRowCarriesTheColourOfItsWord(t *testing.T) {
	t.Parallel()
	row := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true}, false)
	if row.StatusIntent != components.IntentProgress {
		t.Errorf("a picking order's row has intent %q, want %q", row.StatusIntent, components.IntentProgress)
	}
	ready := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPending), Committed: true}, false)
	if ready.StatusIntent != components.IntentWarn {
		t.Errorf("a paid pending order's row has intent %q, want %q", ready.StatusIntent, components.IntentWarn)
	}
}
