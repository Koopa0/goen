package orders

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/components"
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

func TestOrderRowCarriesTheColourOfItsWord(t *testing.T) {
	t.Parallel()
	row := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true})
	if row.StatusIntent != components.IntentProgress {
		t.Errorf("a picking order's row has intent %q, want %q", row.StatusIntent, components.IntentProgress)
	}
	ready := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPending), Committed: true})
	if ready.StatusIntent != components.IntentWarn {
		t.Errorf("a paid pending order's row has intent %q, want %q", ready.StatusIntent, components.IntentWarn)
	}
}
