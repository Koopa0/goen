package returns

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/refund"
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

// The queue's retry control and its repair notice read these two flags; the
// refunds desk only reports the payout, so the mapping is this desk's.
func TestAnUnfitPayoutIsTheOnlyBlockedRow(t *testing.T) {
	t.Parallel()
	unfit := fmt.Errorf("%w: the payout does not fit the return", refund.ErrRefused)
	for _, tt := range []struct {
		name   string
		payout refund.Payout
		want   admin.Return
	}{
		{
			name:   "owed",
			payout: refund.Payout{CardCents: 150000, CreditCents: 50000, Owed: true},
			want:   admin.Return{CardRefundCents: 150000, CreditRefundCents: 50000, PayoutOutstanding: true},
		},
		{
			name:   "unfit",
			payout: refund.Payout{CardCents: 149900, CreditCents: 50100, Owed: true, Unfit: unfit},
			want:   admin.Return{CardRefundCents: 149900, CreditRefundCents: 50100, PayoutOutstanding: true, PayoutBlocked: true},
		},
		{
			name:   "settled",
			payout: refund.Payout{CardCents: 200000},
			want:   admin.Return{CardRefundCents: 200000},
		},
	} {
		var got admin.Return
		showPayout(&got, &tt.payout)
		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("showPayout(%s) mismatch (-want +got):\n%s", tt.name, diff)
		}
	}
}
