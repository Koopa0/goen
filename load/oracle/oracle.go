// Package oracle checks post-run invariants for commerce load profiles (#333).
package oracle

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VariantSnapshot is the stock picture oracle checks use.
type VariantSnapshot struct {
	VariantID     uuid.UUID
	StockQuantity int64
	SafetyStock   int64
	SoldCommitted int64
	ActiveHolds   int64
}

// SellableUnits is stock the storefront may still sell.
func (v VariantSnapshot) SellableUnits() int64 {
	available := v.StockQuantity - v.SafetyStock
	if available < 0 {
		return 0
	}
	return available
}

// ConsumedUnits is committed sales plus inventory still held for checkout.
func (v VariantSnapshot) ConsumedUnits() int64 {
	return v.SoldCommitted + v.ActiveHolds
}

// Oversell reports whether committed plus held units exceed sellable stock.
func (v VariantSnapshot) Oversell() bool {
	return v.ConsumedUnits() > v.SellableUnits()
}

// LoadVariantSnapshot reads the oracle inputs for one variant.
func LoadVariantSnapshot(ctx context.Context, db *pgxpool.Pool, variantID uuid.UUID) (VariantSnapshot, error) {
	if db == nil {
		return VariantSnapshot{}, errors.New("oracle: database pool is required")
	}
	var snap VariantSnapshot
	snap.VariantID = variantID
	err := db.QueryRow(ctx, `
		SELECT stock_quantity, safety_stock
		FROM product_variants
		WHERE id = $1`, variantID).Scan(&snap.StockQuantity, &snap.SafetyStock)
	if err != nil {
		return VariantSnapshot{}, fmt.Errorf("oracle: read variant stock: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(ol.quantity), 0)
		FROM order_lines ol
		JOIN orders o ON o.id = ol.order_id
		JOIN product_variants pv ON pv.sku = ol.sku
		WHERE pv.id = $1
		  AND o.fulfillment_status <> 'cancelled'`, variantID).Scan(&snap.SoldCommitted); err != nil {
		return VariantSnapshot{}, fmt.Errorf("oracle: count committed lines: %w", err)
	}
	if err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(r.quantity), 0)
		FROM inventory_reservations r
		WHERE r.variant_id = $1
		  AND r.state = 'held'
		  AND r.expires_at > now()`, variantID).Scan(&snap.ActiveHolds); err != nil {
		return VariantSnapshot{}, fmt.Errorf("oracle: count active holds: %w", err)
	}
	return snap, nil
}

// CheckNoOversell fails when sold plus held units exceed sellable stock.
func CheckNoOversell(ctx context.Context, db *pgxpool.Pool, variantID uuid.UUID) error {
	snap, err := LoadVariantSnapshot(ctx, db, variantID)
	if err != nil {
		return err
	}
	if snap.Oversell() {
		return fmt.Errorf("oracle: oversell on variant %s: consumed %d > sellable %d (stock=%d safety=%d sold=%d holds=%d)",
			variantID, snap.ConsumedUnits(), snap.SellableUnits(),
			snap.StockQuantity, snap.SafetyStock, snap.SoldCommitted, snap.ActiveHolds)
	}
	return nil
}

// DuplicateCheckout reports checkout attempts that produced more than one order.
type DuplicateCheckout struct {
	IdempotencyKey string
	OrderCount     int
}

// FindDuplicateCheckouts returns idempotency keys mapped to multiple orders.
func FindDuplicateCheckouts(ctx context.Context, db *pgxpool.Pool) ([]DuplicateCheckout, error) {
	if db == nil {
		return nil, errors.New("oracle: database pool is required")
	}
	rows, err := db.Query(ctx, `
		SELECT ca.idempotency_key, COUNT(DISTINCT ca.order_id) AS order_count
		FROM checkout_attempts ca
		WHERE ca.order_id IS NOT NULL
		GROUP BY ca.idempotency_key
		HAVING COUNT(DISTINCT ca.order_id) > 1`)
	if err != nil {
		return nil, fmt.Errorf("oracle: scan duplicate checkouts: %w", err)
	}
	defer rows.Close()
	var out []DuplicateCheckout
	for rows.Next() {
		var dup DuplicateCheckout
		if scanErr := rows.Scan(&dup.IdempotencyKey, &dup.OrderCount); scanErr != nil {
			return nil, fmt.Errorf("oracle: read duplicate row: %w", scanErr)
		}
		out = append(out, dup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oracle: iterate duplicate rows: %w", err)
	}
	return out, nil
}

// CheckNoDuplicateOrders fails when one idempotency key produced multiple orders.
func CheckNoDuplicateOrders(ctx context.Context, db *pgxpool.Pool) error {
	dups, err := FindDuplicateCheckouts(ctx, db)
	if err != nil {
		return err
	}
	if len(dups) > 0 {
		return fmt.Errorf("oracle: %d idempotency keys produced multiple orders (first %s -> %d orders)",
			len(dups), dups[0].IdempotencyKey, dups[0].OrderCount)
	}
	return nil
}

// DegradedWork checks a load run did useful work while dependencies were impaired.
// A cache outage cannot pass solely because every request was rejected.
func DegradedWork(successful, total, minSuccess int64) error {
	if total <= 0 {
		return errors.New("oracle: no requests recorded for degraded-work check")
	}
	if successful < minSuccess {
		return fmt.Errorf("oracle: degraded work %d successful of %d total, want at least %d successes",
			successful, total, minSuccess)
	}
	return nil
}

// FlashSaleVariantID is the pinned limited-stock variant from load/fixtures/catalog.sql.
var FlashSaleVariantID = uuid.MustParse("a3330006-0000-4000-8000-000000000006")

// OpenPool opens a PostgreSQL pool for oracle checks against the load database.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, func(), error) {
	if databaseURL == "" {
		return nil, nil, errors.New("oracle: database URL is required")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("oracle: parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("oracle: open pool: %w", err)
	}
	if pingErr := pool.Ping(ctx); pingErr != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("oracle: ping database: %w", pingErr)
	}
	stop := func() { pool.Close() }
	return pool, stop, nil
}

// MustParseUUID parses a UUID or returns a wrapped error for CLI callers.
func MustParseUUID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("oracle: parse uuid %q: %w", raw, err)
	}
	return id, nil
}
