//go:build integration

package oracle_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/load/oracle"
)

func TestReview353LegitimateHoldIsNotOversell(t *testing.T) {
	t.Parallel()

	t.Run("empty fixture passes", func(t *testing.T) {
		t.Parallel()
		pool := seededPool(t)
		if err := oracle.CheckNoOversell(t.Context(), pool, oracle.FlashSaleVariantID); err != nil {
			t.Fatalf("empty oracle: %v", err)
		}
	})

	t.Run("pending checkout passes", func(t *testing.T) {
		t.Parallel()
		pool := seededPool(t)
		ctx := t.Context()
		placeFlashOrder(t, pool, ctx, 2)
		snap, err := oracle.LoadVariantSnapshot(ctx, pool, oracle.FlashSaleVariantID)
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		if snap.StockQuantity != 1 || snap.SoldCommitted != 2 || snap.ActiveHolds != 2 {
			t.Fatalf("snapshot = stock %d sold %d holds %d, want 1/2/2", snap.StockQuantity, snap.SoldCommitted, snap.ActiveHolds)
		}
		if err := oracle.CheckNoOversell(ctx, pool, oracle.FlashSaleVariantID); err != nil {
			t.Fatalf("legitimate pending order: %v", err)
		}
	})

	t.Run("consumed hold passes", func(t *testing.T) {
		t.Parallel()
		pool := seededPool(t)
		ctx := t.Context()
		orderID := placeFlashOrder(t, pool, ctx, 1)
		var reservation uuid.UUID
		if err := pool.QueryRow(ctx, `
			SELECT id FROM inventory_reservations
			WHERE order_id = $1 AND variant_id = $2`, orderID, oracle.FlashSaleVariantID).Scan(&reservation); err != nil {
			t.Fatalf("read reservation: %v", err)
		}
		if _, err := pool.Exec(ctx, `SELECT consume_reservation($1)`, reservation); err != nil {
			t.Fatalf("consume reservation: %v", err)
		}
		if err := oracle.CheckNoOversell(ctx, pool, oracle.FlashSaleVariantID); err != nil {
			t.Fatalf("consumed hold: %v", err)
		}
	})

	t.Run("cancelled order releases stock", func(t *testing.T) {
		t.Parallel()
		pool := seededPool(t)
		ctx := t.Context()
		orderID := placeFlashOrder(t, pool, ctx, 1)
		if _, err := pool.Exec(ctx, `
			UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
			WHERE id = $1`, orderID); err != nil {
			t.Fatalf("cancel order: %v", err)
		}
		s := cart.NewStore(pool)
		if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatalf("sweep released holds: %v", err)
		}
		if err := oracle.CheckNoOversell(ctx, pool, oracle.FlashSaleVariantID); err != nil {
			t.Fatalf("cancelled order: %v", err)
		}
	})

	t.Run("planted conservation violation fails", func(t *testing.T) {
		t.Parallel()
		pool := seededPool(t)
		ctx := t.Context()
		orderID := placeFlashOrder(t, pool, ctx, 2)
		if _, err := pool.Exec(ctx, `
			UPDATE order_lines SET quantity = quantity + 2
			WHERE order_id = $1`, orderID); err != nil {
			t.Fatalf("plant extra committed units: %v", err)
		}
		if err := oracle.CheckNoOversell(ctx, pool, oracle.FlashSaleVariantID); err == nil {
			t.Fatal("expected oversell when committed units exceed received inventory")
		}
	})
}

func seededPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.Pool(t)
	seedLoadCatalog(t, pool)
	return pool
}

func seedLoadCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	path := filepath.Join(catalogDir(t), "catalog.sql")
	sql, err := os.ReadFile(path) //nolint:gosec // G304: path is built from this package's fixture tree
	if err != nil {
		t.Fatalf("read load catalog: %v", err)
	}
	if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
		t.Fatalf("seed load catalog: %v", err)
	}
}

func catalogDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "fixtures")
}

func storeRolePool(t *testing.T, pool *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse store pool: %v", err)
	}
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, `SET ROLE store`)
		return execErr
	}
	storePool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	return storePool
}

func placeFlashOrder(t *testing.T, pool *pgxpool.Pool, ctx context.Context, qty int32) uuid.UUID {
	t.Helper()
	s := cart.NewStore(storeRolePool(t, pool))
	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	cartID, err := s.Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if addErr := s.Add(ctx, cartID, oracle.FlashSaleVariantID, qty); addErr != nil {
		t.Fatalf("add flash line: %v", addErr)
	}
	shippingID := shipVersionFor(t, pool, "home_delivery")
	addr := &cart.Address{
		Email: "load-oracle@goen.invalid", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	quote := checkoutQuote(t, s, cartID, shippingID, addr)
	number, placeErr := s.PlaceOrder(
		ctx, cartID, uuid.NullUUID{}, shippingID, addr, nil, "", quote,
		checkoutAttemptKey(fmt.Sprintf("review353-%d-%s", qty, uuid.NewString())),
	)
	if placeErr != nil {
		t.Fatalf("place order: %v", placeErr)
	}
	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order id: %v", err)
	}
	return orderID
}

func shipVersionFor(t *testing.T, pool *pgxpool.Pool, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT v.id FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.code = $1 ORDER BY v.effective_at DESC LIMIT 1`, code).Scan(&id); err != nil {
		t.Fatalf("shipping version for %s: %v", code, err)
	}
	return id
}

func checkoutQuote(
	t *testing.T,
	s *cart.Store,
	cartID uuid.UUID,
	shippingID uuid.UUID,
	addr *cart.Address,
) cart.CheckoutQuoteID {
	t.Helper()
	view, err := s.View(t.Context(), cartID)
	if err != nil {
		t.Fatalf("read cart quote: %v", err)
	}
	delivery, err := s.QuoteShipping(t.Context(), shippingID, view.SubtotalCents, addr.PostalCode)
	if err != nil {
		t.Fatalf("quote delivery: %v", err)
	}
	shipping, err := delivery.Total()
	if err != nil {
		t.Fatalf("total shipping quote: %v", err)
	}
	lines := make([]cart.CheckoutQuoteLine, 0, len(view.Lines))
	for i := range view.Lines {
		line := &view.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			t.Fatalf("parse quoted variant: %v", parseErr)
		}
		lines = append(lines, cart.CheckoutQuoteLine{
			VariantID: variantID,
			Quantity:  line.Quantity,
			UnitCents: line.UnitCents,
		})
	}
	id, err := (cart.CheckoutQuote{
		CartID:            cartID,
		Lines:             lines,
		ShippingVersionID: shippingID,
		ShippingCents:     shipping,
	}).ID()
	if err != nil {
		t.Fatalf("build checkout quote: %v", err)
	}
	return id
}

func checkoutAttemptKey(label string) string {
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

func TestStockRunRequiresFreshSingleEffects(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"none", "stale", "missing hold", "wrong amount", "extra order"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			pool := seededPool(t)
			ctx := t.Context()
			started := time.Now().UTC().Add(-time.Second)
			runID := "oracle-" + uuid.NewString()
			orders := make([]evidenceOrder, 0, 2)
			var anchor uuid.UUID
			for i := range 2 {
				id := placeFlashOrder(t, pool, ctx, 1)
				buyer := "0"
				if i == 0 {
					anchor, buyer = id, "replay"
				}
				if _, err := pool.Exec(ctx, `UPDATE order_private_data SET email=$2 WHERE order_id=$1`, id, "load-"+runID+"-"+buyer+"@goen.invalid"); err != nil {
					t.Fatal(err)
				}
				var event evidenceOrder
				if err := pool.QueryRow(ctx, `SELECT o.order_number,a.idempotency_key FROM orders o JOIN checkout_attempts a ON a.order_id=o.id WHERE o.id=$1`, id).Scan(&event.number, &event.key); err != nil {
					t.Fatal(err)
				}
				orders = append(orders, event)
			}
			switch mutation {
			case "stale":
				started = time.Now().UTC().Add(time.Hour)
			case "missing hold":
				if _, err := pool.Exec(ctx, `DELETE FROM inventory_reservations WHERE order_id=$1`, anchor); err != nil {
					t.Fatal(err)
				}
			case "wrong amount":
				if _, err := pool.Exec(ctx, `UPDATE orders SET shipping_cents=shipping_cents+1 WHERE id=$1`, anchor); err != nil {
					t.Fatal(err)
				}
			case "extra order":
				extra := placeFlashOrder(t, pool, ctx, 1)
				if _, err := pool.Exec(ctx, `UPDATE order_private_data SET email=$2 WHERE order_id=$1`, extra, "load-"+runID+"-replay@goen.invalid"); err != nil {
					t.Fatal(err)
				}
			}
			run, err := oracle.ReadStockRun(strings.NewReader(stockEvidence(t, runID, started, orders)), runID)
			if err != nil {
				t.Fatal(err)
			}
			err = oracle.CheckStockRun(ctx, pool, run)
			if mutation == "none" && err != nil {
				t.Fatal(err)
			}
			if mutation != "none" && err == nil {
				t.Fatal("planted incorrect run accepted")
			}
		})
	}
}
