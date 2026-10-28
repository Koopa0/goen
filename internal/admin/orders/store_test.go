package orders

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/widget"
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

// A move out of a cancelled order is told apart from any other illegal move,
// by the order the lock found and not by the constraint alone.
func TestAdvanceRefusal(t *testing.T) {
	t.Parallel()
	legal := &pgconn.PgError{Code: "23514", ConstraintName: "orders_legal_transition"}
	unfunded := &pgconn.PgError{Code: "23514", ConstraintName: "orders_funded_to_leave_pending"}
	for _, tt := range []struct {
		name      string
		err       error
		from      order.FulfillmentStatus
		cancelled bool
	}{
		{"illegal move out of a live order", legal, order.FulfillmentPicking, false},
		{"illegal move out of a cancelled order", legal, order.FulfillmentCancelled, true},
		{"another rule out of a cancelled order", unfunded, order.FulfillmentCancelled, false},
		{"not a constraint", errors.New("connection lost"), order.FulfillmentCancelled, false},
	} {
		got := advanceRefusal(tt.err, tt.from)
		if errors.Is(got, ErrCancelled) != tt.cancelled {
			t.Errorf("%s: cancelled = %t, want %t", tt.name, !tt.cancelled, tt.cancelled)
		}
		if errors.Is(got, tt.err) != true {
			t.Errorf("%s: %v no longer wraps %v", tt.name, got, tt.err)
		}
	}
	if err := advanceRefusal(legal, order.FulfillmentPicking); !errors.Is(err, ErrRefused) {
		t.Errorf("a refused move = %v, want ErrRefused", err)
	}
	if err := advanceRefusal(errors.New("connection lost"), order.FulfillmentPicking); errors.Is(err, ErrRefused) {
		t.Errorf("a lost connection = %v, which reads as a refusal", err)
	}
}

func TestRowCarriesTheColourOfItsWord(t *testing.T) {
	t.Parallel()
	row := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPicking), Committed: true}, false)
	if row.StatusIntent != widget.IntentProgress {
		t.Errorf("a picking order's row has intent %q, want %q", row.StatusIntent, widget.IntentProgress)
	}
	ready := orderRow(t.Context(), &db.AdminOrdersRow{FulfillmentStatus: string(order.FulfillmentPending), Committed: true}, false)
	if ready.StatusIntent != widget.IntentStaffTurn {
		t.Errorf("a paid pending order's row has intent %q, want %q", ready.StatusIntent, widget.IntentStaffTurn)
	}
}
