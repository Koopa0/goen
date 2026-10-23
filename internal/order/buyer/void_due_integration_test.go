//go:build integration

package buyer_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/outbox"
)

func queuedVoidDues(t *testing.T, number string) []outbox.InvoiceVoidDue {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT payload FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceVoidDue.Name(), number)
	if err != nil {
		t.Fatalf("read invoice.void_due for %s: %v", number, err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatalf("collect invoice.void_due for %s: %v", number, err)
	}
	out := make([]outbox.InvoiceVoidDue, len(payloads))
	for i, p := range payloads {
		if err := json.Unmarshal(p, &out[i]); err != nil {
			t.Fatalf("decode invoice.void_due: %v", err)
		}
	}
	return out
}

// TestStore_CancelOrderNoVoidDue: Store.CancelOrder queues no
// invoice void for an order nobody paid, which has no invoice to void.
func TestStore_CancelOrderNoVoidDue(t *testing.T) {
	number := ordertest.NumberOf(t, pool, ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "void-due-unpaid"), -time.Hour, false))
	if err := buyer.NewStore(storeRolePool(t)).CancelOrder(t.Context(), number); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := queuedVoidDues(t, number); len(got) != 0 {
		t.Errorf("cancelling an unpaid order queued %+v, want nothing", got)
	}
}
