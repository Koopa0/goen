//go:build integration

package returns_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/returns"
)

// TestReturnWritesAnswerALockTimeoutAsAFailure: each write locks the return's
// order first, and that lock timing out is the database not answering, not a
// rule refusing the return.
func TestReturnWritesAnswerALockTimeoutAsAFailure(t *testing.T) {
	ctx, actor := admintest.StaffContext(t, pool)
	staff := uuid.NullUUID{UUID: actor, Valid: true}
	s := storeOver(admintest.LockTimeoutPool(t, pool), admintest.Refunder{})
	for _, tt := range []struct {
		name  string
		write func(id string) error
	}{
		{name: "assess", write: func(id string) error { return s.Assess(ctx, id, "lock timeout", nil) }},
		{name: "decide", write: func(id string) error { return s.Decide(ctx, id, "rejected", "lock timeout", "") }},
		{name: "inspect", write: func(id string) error {
			return s.Inspect(ctx, id, []returns.LineInspection{{OrderLineID: uuid.New(), Received: 1}}, staff)
		}},
		{name: "complete", write: func(id string) error { return s.Complete(ctx, id, "") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, number := admintest.ReturnedOrder(t, pool, 1)
			admintest.HoldRow(t, pool, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, number)
			if err := tt.write(id.String()); errors.Is(err, returns.ErrRefused) || !admintest.LockTimedOut(err) {
				t.Errorf("%s of return %s behind a held order lock = %v, want lock_not_available (55P03) and not ErrRefused",
					tt.name, id, err)
			}
		})
	}
}
