//go:build integration

package admintest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func AdminRolePool(t *testing.T, owner *pgxpool.Pool) *pgxpool.Pool {
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
		t.Fatalf("admin writer role = %q, %v", role, err)
	}
	return p
}

func SubscribeAtSafetyStock(t *testing.T, pool *pgxpool.Pool, variant uuid.UUID) []uuid.UUID {
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
	subscriptions := make([]uuid.UUID, 0, len(i18n.Locales()))
	for _, locale := range i18n.Locales() {
		var id uuid.UUID
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3) RETURNING id`, variant, locale.Tag()+"-"+uuid.NewString()+"@example.com", locale.Tag()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		subscriptions = append(subscriptions, id)
	}
	return subscriptions
}

func AssertRestockQueued(t *testing.T, p *pgxpool.Pool, subscriptions []uuid.UUID) {
	t.Helper()
	rows, err := p.Query(t.Context(), `
		SELECT m.payload, n.email, n.locale, localized_name(p.name, p.name_en, n.locale), p.slug, v.sku
		FROM stock_notifications n
		JOIN outbox_messages m ON m.dedupe_key = n.id::text AND m.topic = $2
		JOIN product_variants v ON v.id = n.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE n.id = ANY($1::uuid[]) AND n.notified_at IS NOT NULL`, subscriptions, outbox.TopicRestocked.Name())
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
		SELECT count(*) FROM stock_notifications WHERE id = ANY($1::uuid[]) AND notified_at IS NULL`,
		subscriptions).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending restock notices = %d, %v", pending, err)
	}
}
