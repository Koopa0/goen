//go:build integration

package stock_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/stock"
)

// TestConcurrentWritesAuditTheValueEachReplaced: two staff change one variant
// at once. Read before the write's own transaction, both audit rows name the
// value the variant had before either write, and the trail shows the second
// change starting from a value it never replaced.
func TestConcurrentWritesAuditTheValueEachReplaced(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	actor := adminID(t)

	var variantID uuid.UUID
	var sku string
	var price int64
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku, pv.price_cents FROM product_variants pv
		WHERE pv.is_active AND pv.compare_at_price_cents IS NULL
		  AND NOT EXISTS (SELECT 1 FROM sale_campaign_products c WHERE c.product_id = pv.product_id)
		ORDER BY pv.sku LIMIT 1`).Scan(&variantID, &sku, &price); err != nil {
		t.Fatalf("read variant: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx),
			`UPDATE product_variants SET price_cents = $2 WHERE id = $1`, variantID, price); err != nil {
			t.Errorf("restore the price of %s: %v", sku, err)
		}
	})

	tests := []struct {
		name   string
		action audit.Action
		column string // the product_variants column both writes change
		before string // that value's key in the audit row's before
		after  string // the key in its after that names the write
		args   func(start int32) [2]int32
		write  func(s *stock.Store, arg int32) error
		leaves func(replaced, arg int32) int32
	}{
		{
			name: "two reprices", action: audit.ActionRepriceVariant,
			column: "price_cents", before: "price_cents", after: "price_cents",
			args:   func(start int32) [2]int32 { return [2]int32{start + 100, start + 200} },
			write:  func(s *stock.Store, arg int32) error { return s.SetPrice(ctx, sku, int64(arg), 0) },
			leaves: func(_, arg int32) int32 { return arg },
		},
		{
			name: "two adjustments", action: audit.ActionAdjustStock,
			column: "stock_quantity", before: "stock", after: "delta",
			args: func(int32) [2]int32 { return [2]int32{3, 5} },
			write: func(s *stock.Store, arg int32) error {
				return s.Adjust(ctx, sku, arg, actor, "audit-before-"+uuid.NewString())
			},
			leaves: func(replaced, arg int32) int32 { return replaced + arg },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var start int32
			if err := pool.QueryRow(ctx, `SELECT `+tt.column+`::integer FROM product_variants WHERE id = $1`,
				variantID).Scan(&start); err != nil {
				t.Fatalf("read %s: %v", tt.column, err)
			}
			args := tt.args(start)

			holder, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin the lock holder: %v", err)
			}
			defer func() { _ = holder.Rollback(context.WithoutCancel(ctx)) }()
			if _, err = holder.Exec(ctx, `SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, variantID); err != nil {
				t.Fatalf("hold the variant's row: %v", err)
			}

			var done [2]chan error
			for i, arg := range args {
				name := fmt.Sprintf("audit-before-%d-%s", i, uuid.NewString()[:8])
				s := adminWriter(t, name)
				done[i] = make(chan error, 1)
				go func() { done[i] <- tt.write(s, arg) }()
				waitUntilBlocked(t, name, done[i])
			}
			if err = holder.Rollback(ctx); err != nil {
				t.Fatalf("release the variant's row: %v", err)
			}
			for i := range done {
				if writeErr := <-done[i]; writeErr != nil {
					t.Fatalf("write %d of %s: %v", args[i], sku, writeErr)
				}
			}

			type change struct{ replaced, arg int32 }
			rows, err := pool.Query(ctx, `
				SELECT (before->>$3)::integer, (after->>$4)::integer FROM audit_events
				WHERE entity_id = $1 AND action = $2 ORDER BY id DESC LIMIT 2`,
				variantID, string(tt.action), tt.before, tt.after)
			if err != nil {
				t.Fatalf("read audit rows: %v", err)
			}
			var got []change
			for rows.Next() {
				var c change
				if scanErr := rows.Scan(&c.replaced, &c.arg); scanErr != nil {
					t.Fatalf("scan audit row: %v", scanErr)
				}
				got = append(got, c)
			}
			if err = rows.Err(); err != nil {
				t.Fatalf("read audit rows: %v", err)
			}
			if len(got) != 2 || got[0].arg+got[1].arg != args[0]+args[1] {
				t.Fatalf("audit rows of %s = %+v, want one for each of %v", tt.action, got, args)
			}
			first, second := got[0], got[1]
			if first.replaced != start {
				first, second = second, first
			}
			if first.replaced != start || second.replaced != tt.leaves(first.replaced, first.arg) {
				t.Errorf("%s audit befores = %d and %d, want %d and then %d, the value the first write left",
					tt.action, first.replaced, second.replaced, start, tt.leaves(start, first.arg))
			}
		})
	}
}

// adminWriter is a stock desk on one connection as the admin role, named so
// the test can see that connection waiting on a lock.
func adminWriter(t *testing.T, name string) *stock.Store {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse writer pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["role"] = "admin"
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open writer pool: %v", err)
	}
	t.Cleanup(p.Close)
	return stock.NewStore(p)
}

func waitUntilBlocked(t *testing.T, name string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			               WHERE application_name = $1 AND wait_event_type = 'Lock')`, name).
			Scan(&waiting); err == nil && waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("writer %s finished before reaching the held row: %v", name, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("writer %s never waited on the held row", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
