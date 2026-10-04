//go:build integration

package stock_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func TestRestockClaimAndOutboxRollBackWithStock(t *testing.T) {
	owner := admintest.Pool(t)
	writer := admintest.AdminRolePool(t, owner)
	ctx := t.Context()
	var variant uuid.UUID
	var quantity int32
	if err := owner.QueryRow(ctx, `
		SELECT id, stock_quantity FROM product_variants
		WHERE is_active AND stock_quantity > safety_stock ORDER BY id LIMIT 1`).Scan(&variant, &quantity); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, quantity); err != nil {
		t.Fatal(err)
	}
	subscriptions := make([]uuid.UUID, 0, len(i18n.Locales()))
	for _, locale := range i18n.Locales() {
		var id uuid.UUID
		if err := owner.QueryRow(ctx, `INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3) RETURNING id`, variant, locale.Tag()+"-rollback@example.com", locale.Tag()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		subscriptions = append(subscriptions, id)
	}
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, quantity-1); err != nil {
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
		FROM product_variants WHERE id = $1`, variant, outbox.TopicRestocked.Name()).Scan(&safety, &claimed, &queued); err != nil || safety != quantity || claimed != 0 || queued != 0 {
		t.Fatalf("after rollback safety/claimed/queued = %d/%d/%d, %v", safety, claimed, queued, err)
	}
	if _, err := writer.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, quantity-1); err != nil {
		t.Fatal(err)
	}
	admintest.AssertRestockQueued(t, owner, subscriptions)
	if _, err := writer.Exec(ctx, `UPDATE product_variants SET safety_stock = $2 WHERE id = $1`, variant, quantity-1); err != nil {
		t.Fatal(err)
	}
	admintest.AssertRestockQueued(t, owner, subscriptions)
}

func TestReactivatingAStockedVariantQueuesWaitingNotices(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, owner)
	writer := admintest.AdminRolePool(t, owner)
	s := stock.NewStore(writer)
	var variant uuid.UUID
	var sku string
	if err := owner.QueryRow(ctx, `
		SELECT v.id, v.sku FROM product_variants v
		WHERE v.is_active
		  AND (SELECT count(*) FROM product_variants sibling
		       WHERE sibling.product_id = v.product_id AND sibling.is_active) > 1
		  AND NOT EXISTS (SELECT 1 FROM sale_campaign_products cp WHERE cp.product_id = v.product_id)
		ORDER BY v.id LIMIT 1`).Scan(&variant, &sku); err != nil {
		t.Fatal(err)
	}
	subscriptions := admintest.SubscribeAtSafetyStock(t, owner, variant)
	if err := s.SetActive(ctx, sku, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(ctx, sku, 1, actor.String(), "inactive-receipt-"+variant.String()); err != nil {
		t.Fatal(err)
	}
	var claimed, queued int
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM stock_notifications WHERE id = ANY($1::uuid[]) AND notified_at IS NOT NULL),
		(SELECT count(*) FROM outbox_messages m JOIN stock_notifications n ON n.id::text = m.dedupe_key
		 WHERE m.topic = $2 AND n.id = ANY($1::uuid[]))`,
		subscriptions, outbox.TopicRestocked.Name()).Scan(&claimed, &queued); err != nil || claimed != 0 || queued != 0 {
		t.Fatalf("inactive receipt claimed/queued = %d/%d, want 0/0: %v", claimed, queued, err)
	}
	if err := s.SetActive(ctx, sku, true); err != nil {
		t.Fatal(err)
	}
	admintest.AssertRestockQueued(t, owner, subscriptions)
	if err := s.SetActive(ctx, sku, true); err != nil {
		t.Fatal(err)
	}
	admintest.AssertRestockQueued(t, owner, subscriptions)
}
