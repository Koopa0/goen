//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func restockAdminPool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := owner.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE admin`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("restock writer role = %q, %v", role, err)
	}
	return p
}

func waitForRestock(t *testing.T, variant uuid.UUID) {
	t.Helper()
	var safety int32
	if err := pool.QueryRow(t.Context(), `
		SELECT safety_stock FROM product_variants WHERE id = $1`, variant).Scan(&safety); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(t.Context()),
			`UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, safety); err != nil {
			t.Errorf("restore safety stock: %v", err)
		}
	})
	if _, err := pool.Exec(t.Context(), `
		UPDATE product_variants SET safety_stock = stock_quantity WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3)`, variant, locale.Tag()+"-"+variant.String()+"@example.com", locale.Tag()); err != nil {
			t.Fatal(err)
		}
	}
}

func assertRestockQueued(t *testing.T, p *pgxpool.Pool, variant uuid.UUID) {
	t.Helper()
	rows, err := p.Query(t.Context(), `
		SELECT m.payload, n.email, n.locale, localized_name(p.name, p.name_en, n.locale), p.slug, v.sku
		FROM stock_notifications n
		JOIN outbox_messages m ON m.dedupe_key = n.id::text AND m.topic = $2
		JOIN product_variants v ON v.id = n.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE n.variant_id = $1 AND n.notified_at IS NOT NULL
		  AND n.email = n.locale || '-' || $1::text || '@example.com'`, variant, outbox.TopicRestocked.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var payload []byte
		var want, got email.RestockNotice
		if err := rows.Scan(&payload, &want.Email, &want.Locale, &want.ProductName, &want.Slug, &want.SKU); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if got != want || seen[got.Locale] {
			t.Fatalf("restock payload = %+v, want %+v once", got, want)
		}
		seen[got.Locale] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(i18n.Locales()) {
		t.Fatalf("notified locales = %v, want both", seen)
	}
	var pending int
	if err := p.QueryRow(t.Context(), `
		SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		variant).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending restock notices = %d, %v", pending, err)
	}
}

func TestRestockClaimAndOutboxRollBackWithStock(t *testing.T) {
	owner := admintest.Pool(t)
	writer := restockAdminPool(t, owner)
	ctx := t.Context()
	var variant uuid.UUID
	var stock int32
	if err := owner.QueryRow(ctx, `
		SELECT id, stock_quantity FROM product_variants
		WHERE is_active AND stock_quantity > safety_stock ORDER BY id LIMIT 1`).Scan(&variant, &stock); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, stock); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		if _, err := owner.Exec(ctx, `INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3)`, variant, locale.Tag()+"-"+variant.String()+"@example.com", locale.Tag()); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, stock-1); err != nil {
		t.Fatal(err)
	}
	var claimed, queued int
	if err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NOT NULL),
		(SELECT count(*) FROM outbox_messages WHERE topic = $2)`, variant, outbox.TopicRestocked.Name()).Scan(&claimed, &queued); err != nil || claimed != 2 || queued != 2 {
		t.Fatalf("inside stock transaction claimed/queued = %d/%d, %v", claimed, queued, err)
	}
	if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil {
		t.Fatal(err)
	}
	var safety int32
	if err := owner.QueryRow(ctx, `SELECT safety_stock,
		(SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NOT NULL),
		(SELECT count(*) FROM outbox_messages WHERE topic = $2)
		FROM product_variants WHERE id = $1`, variant, outbox.TopicRestocked.Name()).Scan(&safety, &claimed, &queued); err != nil || safety != stock || claimed != 0 || queued != 0 {
		t.Fatalf("after rollback safety/claimed/queued = %d/%d/%d, %v", safety, claimed, queued, err)
	}
	if _, err := writer.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, stock-1); err != nil {
		t.Fatal(err)
	}
	assertRestockQueued(t, owner, variant)
	if _, err := writer.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, stock-1); err != nil {
		t.Fatal(err)
	}
	assertRestockQueued(t, owner, variant)
}
