//go:build integration

package cart_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
)

// TestCreditPayingTheWholeOrderQueuesItsInvoiceAtCheckout: credit that pays
// everything is money received at checkout, so the invoice is due then and not
// when the shop picks the order. Credit that pays part waits for the card.
func TestCreditPayingTheWholeOrderQueuesItsInvoiceAtCheckout(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	place := func(creditCents int64, key string) string {
		t.Helper()
		userID := creditedCustomer(t, creditCents)
		id := newCart(t, s)
		if err := s.Add(ctx, id, vid, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{UUID: userID, Valid: true}, shipID,
			&cart.Address{
				Email: "due@example.com", Name: "王小明", Phone: "0912345678",
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			}, "", key)
		if err != nil {
			t.Fatalf("place: %v", err)
		}
		return number
	}
	whole := place(100000000, "invoice-due-whole")
	part := place(100, "invoice-due-part")

	if got := queuedDues(t, part); len(got) != 0 {
		t.Errorf("credit paying part of %s queued %d invoices at checkout, want 0", part, len(got))
	}
	got := queuedDues(t, whole)
	if len(got) != 1 || got[0].OrderNumber != whole || got[0].Trigger != "commit:"+whole {
		t.Fatalf("credit paying all of %s queued %+v, want one due with its commit", whole, got)
	}

	gateway, err := invoice.NewGateway("", "", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	if err := invoice.NewStore(adminRole(t), gateway).ClaimDue(ctx, &got[0]); err != nil {
		t.Fatalf("claim the invoice of a pending order credit paid in full: %v", err)
	}
	var kind, status string
	if err := pool.QueryRow(ctx, `
		SELECT op.actor_kind, o.fulfillment_status
		FROM invoice_operations op JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = 'issue'`, whole).Scan(&kind, &status); err != nil {
		t.Fatalf("read the claimed issue of %s: %v", whole, err)
	}
	if kind != "system" || status != "pending" {
		t.Errorf("claimed issue = %s on a %s order, want system on a pending one", kind, status)
	}
}

func queuedDues(t *testing.T, number string) []invoice.Due {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT payload FROM outbox_messages WHERE topic = $1 AND dedupe_key = $2`,
		outbox.TopicInvoiceDue, number)
	if err != nil {
		t.Fatalf("read invoice.due for %s: %v", number, err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		t.Fatalf("collect invoice.due for %s: %v", number, err)
	}
	out := make([]invoice.Due, len(payloads))
	for i, p := range payloads {
		if err := json.Unmarshal(p, &out[i]); err != nil {
			t.Fatalf("decode invoice.due: %v", err)
		}
	}
	return out
}

// adminRole is a pool connected as the back office's role, which holds the
// claim door's EXECUTE; the suite's own pool owns everything and would hide a
// missing grant.
func adminRole(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse admin-role pool: %v", err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE admin")
		return roleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open admin-role pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
