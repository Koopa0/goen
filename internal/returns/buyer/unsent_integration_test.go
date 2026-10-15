//go:build integration

package buyer_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/returns/buyer"
)

// The units the shop refunded because it could not send them never reached
// the customer: they do not use up what the customer may return of what did
// arrive, and the refund is not a request the customer made.
func TestAnUnsentRefundLeavesTheShippedUnitsReturnable(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
	if err := refunds.NewStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil).RefundUnsent(ctx, o.Number,
		&refunds.UnsentRequest{Units: map[uuid.UUID]int32{o.LineB: 2}, Reason: string(returns.UnsentDamaged), Note: "外盒破損"}); err != nil {
		t.Fatalf("refund the unsent units: %v", err)
	}
	got, err := buyer.NewStore(admintest.StoreRolePool(t, pool)).Order(ctx, o.Number)
	if err != nil {
		t.Fatalf("read the return form: %v", err)
	}
	returnable := map[string]int32{}
	for _, l := range got.Lines {
		returnable[l.ID] = l.Returnable
	}
	if returnable[o.LineA.String()] != 1 || returnable[o.LineB.String()] != 1 {
		t.Errorf("returnable A/B = %d/%d, want the one of each that arrived",
			returnable[o.LineA.String()], returnable[o.LineB.String()])
	}
	if len(got.Existing) != 0 {
		t.Errorf("the customer's page lists %+v, want no request: the shop refunded what it could not send", got.Existing)
	}
}
