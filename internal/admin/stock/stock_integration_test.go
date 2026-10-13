//go:build integration

package stock_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
)

func TestStockMovesOnlyThroughTheLedger(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "test-" + uuid.NewString()
	if err := s.Adjust(ctx, sku, 7, actor, key); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before+7 {
		t.Errorf("stock went %d -> %d, want %d", before, after, before+7)
	}

	var delta int32
	var reason string
	var hasActor bool
	if err := pool.QueryRow(ctx, `
		SELECT m.delta, m.reason, m.actor_user_id IS NOT NULL
		FROM inventory_movements m WHERE m.idempotency_key = $1`, key).
		Scan(&delta, &reason, &hasActor); err != nil {
		t.Fatalf("the adjustment left no movement row: %v", err)
	}
	if delta != 7 || reason != "adjustment" || !hasActor {
		t.Errorf("movement = %d/%q/actor:%v, want 7/adjustment/actor:true", delta, reason, hasActor)
	}
}

func TestAdjustmentIsIdempotent(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position DESC LIMIT 1`).
		Scan(&sku); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "dup-" + uuid.NewString()
	if err := s.Adjust(ctx, sku, 5, actor, key); err != nil {
		t.Fatalf("first adjust: %v", err)
	}
	var afterFirst int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterFirst); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	// A replay of the form that already booked it is that adjustment's success,
	// not a refusal: the staff member who sees "refused" re-enters it.
	if err := s.Adjust(ctx, sku, 5, actor, key); err != nil {
		t.Errorf("replaying an applied adjustment = %v, want the earlier success", err)
	}
	// The key is spent by that movement alone.
	if err := s.Adjust(ctx, sku, 6, actor, key); !errors.Is(err, stock.ErrRefused) {
		t.Errorf("reusing the key for a different adjustment = %v, want ErrRefused", err)
	}
	var afterSecond int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterSecond); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if afterSecond != afterFirst {
		t.Errorf("stock moved again on the repeat: %d -> %d", afterFirst, afterSecond)
	}
}

func TestTheStockLedgerCanBeRead(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)

	// A variant that ALREADY holds stock: at a prior balance of zero the running total
	// and the movement's own delta are the same number and the assertion proves nothing.
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE stock_quantity > 0
		ORDER BY stock_quantity DESC LIMIT 1`).Scan(&sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}

	before, err := s.Movements(ctx, sku, time.Now())
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	if before.Stock <= 0 {
		t.Fatalf("the chosen variant holds %d — the running total would equal the "+
			"delta and prove nothing", before.Stock)
	}

	// A key unique to this run, or the second run of this suite is a no-op.
	key := "ledger-test-" + uuid.NewString()
	if adjErr := s.Adjust(ctx, sku, 7, staff.String(), key); adjErr != nil {
		t.Fatalf("AdjustStock: %v", adjErr)
	}

	after, err := s.Movements(ctx, sku, time.Now())
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	if len(after.Rows) != len(before.Rows)+1 {
		t.Fatalf("%d rows after one adjustment, had %d", len(after.Rows), len(before.Rows))
	}

	newest := after.Rows[0]
	if newest.Delta != 7 {
		t.Errorf("the newest movement is %d, want +7", newest.Delta)
	}
	if newest.Reason != inventory.ReasonAdjustment || newest.ReasonText(ctx) != "人工調整" {
		t.Errorf("the movement reads as %q / %q", newest.Reason, newest.ReasonText(ctx))
	}
	if newest.By(ctx) == "系統" {
		t.Error("a hand adjustment is attributed to the system")
	}
	if newest.DeltaText() != "+7" {
		t.Errorf("the delta reads %q, want +7 — a bare 7 is half the story",
			newest.DeltaText())
	}
	if newest.Running != before.Stock+7 {
		t.Errorf("the newest running total is %d, want %d — the stock before plus "+
			"this movement", newest.Running, before.Stock+7)
	}
	if newest.Running == newest.Delta {
		t.Error("the running total equals this movement's own delta, so it is not " +
			"running over the ledger at all")
	}
	if newest.Running != after.Stock {
		t.Errorf("the newest running total is %d and the variant holds %d",
			newest.Running, after.Stock)
	}
}

func TestAReceiptIsFiledAsAReceiptAndNotAnAdjustment(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-" + uuid.NewString()
	if err := s.Receive(ctx, sku, 12, actor, key); err != nil {
		t.Fatalf("receive: %v", err)
	}

	var delta int32
	var reason, source string
	var hasActor bool
	if err := pool.QueryRow(ctx, `
		SELECT m.delta, m.reason, coalesce(m.source_type, ''), m.actor_user_id IS NOT NULL
		FROM inventory_movements m WHERE m.idempotency_key = $1`, key).
		Scan(&delta, &reason, &source, &hasActor); err != nil {
		t.Fatalf("the receipt left no movement row: %v", err)
	}
	if delta != 12 || reason != "receipt" || source != "admin" || !hasActor {
		t.Errorf("movement = %d/%q/%q/actor:%v, want 12/receipt/admin/actor:true",
			delta, reason, source, hasActor)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before+12 {
		t.Errorf("stock went %d -> %d, want %d", before, after, before+12)
	}
}

func TestAReceiptIsIdempotent(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT sku FROM product_variants WHERE is_active ORDER BY position DESC LIMIT 1`).
		Scan(&sku); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-dup-" + uuid.NewString()
	if err := s.Receive(ctx, sku, 4, actor, key); err != nil {
		t.Fatalf("first receipt: %v", err)
	}
	var afterFirst int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterFirst); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	// A replay of the form that booked it is that delivery's success, and
	// books nothing; the same key for a different delivery stays refused.
	if err := s.Receive(ctx, sku, 4, actor, key); err != nil {
		t.Errorf("replaying an applied receipt = %v, want the earlier success", err)
	}
	if err := s.Receive(ctx, sku, 5, actor, key); !errors.Is(err, stock.ErrRefused) {
		t.Errorf("reusing the key for a different delivery = %v, want ErrRefused", err)
	}
	var afterSecond int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&afterSecond); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if afterSecond != afterFirst {
		t.Errorf("stock moved again on the repeat: %d -> %d", afterFirst, afterSecond)
	}
}

func TestAReceiptCannotTakeStockAway(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := stock.NewStore(pool)
	actor := adminID(t)

	var sku string
	var before int32
	if err := pool.QueryRow(ctx, `
		SELECT sku, stock_quantity FROM product_variants
		WHERE is_active AND stock_quantity > 5 ORDER BY position LIMIT 1`).Scan(&sku, &before); err != nil {
		t.Fatalf("read variant: %v", err)
	}

	key := "receipt-neg-" + uuid.NewString()
	if err := s.Receive(ctx, sku, -3, actor, key); !errors.Is(err, stock.ErrRefused) {
		t.Errorf("a negative receipt gave %v, want ErrRefused — a correction filed "+
			"as a delivery is the distinction this door exists to draw", err)
	}
	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE sku = $1`, sku).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before {
		t.Errorf("the refused receipt moved stock %d -> %d", before, after)
	}
}

func TestARefusedStockAdjustmentKeepsWhatWasTyped(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(stock.NewStore(pool))
	var sku string
	if err := pool.QueryRow(ctx, `SELECT sku FROM product_variants ORDER BY sku LIMIT 1`).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"sku": {sku}, "delta": {"12x"}, "return": {"/admin/stock"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/adjust", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Adjust)(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unreadable adjustment answered %d, want 422", w.Code)
	}
	// The row may be on a later page of a long list, in which case the banner
	// carries the sentence; either way the refusal is said and the page is 422.
	if !strings.Contains(w.Body.String(), i18n.T(ctx, i18n.KeyAdminStockDeltaError)) {
		t.Error("the refused adjustment does not say why")
	}
}

func TestRestockingTellsEverybodyWhoAsked(t *testing.T) {
	ctx := t.Context()
	owner := admintest.Pool(t)
	s := stock.NewStore(admintest.AdminRolePool(t, owner))

	var vid uuid.UUID
	var sku string
	if err := owner.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active ORDER BY pv.id LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a variant: %v", err)
	}
	emptyTheShelf(t, owner, vid, "restock-test-empty")
	for _, address := range []string{"waiting1@example.com", "waiting2@example.com"} {
		if _, err := owner.Exec(ctx,
			`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, $2)`,
			vid, address); err != nil {
			t.Fatalf("record interest from %s: %v", address, err)
		}
	}

	var actor uuid.UUID
	if err := owner.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "restock-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: actor.String(), Role: user.RoleAdmin})

	if err := s.Adjust(staffCtx, sku, 10, actor.String(), "restock-test-1"); err != nil {
		t.Fatalf("restock: %v", err)
	}

	var enqueued int
	if err := owner.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages m
		JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
		WHERE m.topic = 'catalogue.restocked' AND sn.variant_id = $1`, vid).Scan(&enqueued); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if enqueued != 2 {
		t.Errorf("%d restock notices enqueued, want 2", enqueued)
	}

	var pending int
	if err := owner.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Errorf("%d notices are still pending after the restock", pending)
	}

	if err := s.Adjust(staffCtx, sku, 5, actor.String(), "restock-test-2"); err != nil {
		t.Fatalf("second restock: %v", err)
	}
	var after int
	if err := owner.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages m
		JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
		WHERE m.topic = 'catalogue.restocked' AND sn.variant_id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("count notices again: %v", err)
	}
	if after != enqueued {
		t.Errorf("a second restock enqueued %d more notices", after-enqueued)
	}
}

func TestAnAdjustmentBelowTheThresholdTellsNobody(t *testing.T) {
	ctx := t.Context()
	owner := admintest.Pool(t)
	s := stock.NewStore(admintest.AdminRolePool(t, owner))

	var vid uuid.UUID
	var sku string
	if err := owner.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active ORDER BY pv.id DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a variant: %v", err)
	}
	emptyTheShelf(t, owner, vid, "threshold-empty")
	if _, err := owner.Exec(ctx,
		`UPDATE product_variants SET safety_stock = 5 WHERE id = $1`, vid); err != nil {
		t.Fatalf("set safety stock: %v", err)
	}
	if _, err := owner.Exec(ctx,
		`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, 'threshold@example.com')`,
		vid); err != nil {
		t.Fatalf("record interest: %v", err)
	}

	var actor uuid.UUID
	if err := owner.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "threshold-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: actor.String(), Role: user.RoleAdmin})

	if err := s.Adjust(staffCtx, sku, 3, actor.String(), "threshold-test-1"); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	var pending int
	if err := owner.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 1 {
		t.Errorf("a notice was spent on stock nobody can buy (%d pending, want 1)", pending)
	}

	if err := s.Adjust(staffCtx, sku, 5, actor.String(), "threshold-test-2"); err != nil {
		t.Fatalf("second adjust: %v", err)
	}
	if err := owner.QueryRow(ctx,
		`SELECT count(*) FROM stock_notifications WHERE variant_id = $1 AND notified_at IS NULL`,
		vid).Scan(&pending); err != nil {
		t.Fatalf("count pending again: %v", err)
	}
	if pending != 0 {
		t.Errorf("crossing the threshold told nobody (%d still pending)", pending)
	}
}

func TestARestockNoticeNamesTheProductInTheReadersLanguage(t *testing.T) {
	ctx := t.Context()
	owner := admintest.Pool(t)
	s := stock.NewStore(admintest.AdminRolePool(t, owner))

	var vid uuid.UUID
	var sku string
	if err := owner.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active AND p.name_en IS NOT NULL
		  AND p.name_en <> p.name
		ORDER BY pv.id LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a translated variant: %v", err)
	}
	emptyTheShelf(t, owner, vid, "restock-locale-empty")

	waiting := map[string]string{
		"zh-Hant": "zh-waiting@example.com",
		"en":      "en-waiting@example.com",
	}
	for locale, address := range waiting {
		if _, err := owner.Exec(ctx, `
			INSERT INTO stock_notifications (variant_id, email, locale)
			VALUES ($1, $2, $3)`, vid, address, locale); err != nil {
			t.Fatalf("record interest from %s: %v", address, err)
		}
	}

	var actor uuid.UUID
	if err := owner.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ($1, 'admin', '補貨')
		RETURNING id`, "restock-locale-"+sku+"@goen.invalid").Scan(&actor); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: actor.String(), Role: user.RoleAdmin})

	if err := s.Adjust(staffCtx, sku, 10, actor.String(), "restock-locale-1"); err != nil {
		t.Fatalf("restock: %v", err)
	}

	var zhName, enName string
	if err := owner.QueryRow(ctx, `
		SELECT p.name, p.name_en FROM products p
		JOIN product_variants pv ON pv.product_id = p.id WHERE pv.id = $1`,
		vid).Scan(&zhName, &enName); err != nil {
		t.Fatalf("read both names: %v", err)
	}

	for locale, address := range waiting {
		var payload string
		if err := owner.QueryRow(ctx, `
			SELECT m.payload::text FROM outbox_messages m
			JOIN stock_notifications sn ON sn.id::text = m.dedupe_key
			WHERE m.topic = 'catalogue.restocked' AND sn.email = $1`,
			address).Scan(&payload); err != nil {
			t.Fatalf("read the %s payload: %v", locale, err)
		}
		want, other := zhName, enName
		if locale == "en" {
			want, other = enName, zhName
		}
		if !strings.Contains(payload, want) {
			t.Errorf("the %s letter does not name the product as %q: %s",
				locale, want, payload)
		}
		if strings.Contains(payload, other) {
			t.Errorf("the %s letter names the product as %q, the other language: %s",
				locale, other, payload)
		}
	}
}

// emptyTheShelf takes a variant down to zero. It reads the quantity first because
// inventory_movements_delta_non_zero refuses a movement of nothing.
func emptyTheShelf(t *testing.T, owner *pgxpool.Pool, vid uuid.UUID, key string) {
	t.Helper()
	var onShelf int32
	if err := owner.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&onShelf); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if onShelf > 0 {
		if _, err := owner.Exec(t.Context(),
			`SELECT record_inventory_movement($1, $2, 'adjustment', $3, 'admin', NULL, NULL)`,
			vid, -onShelf, key); err != nil {
			t.Fatalf("empty the shelf: %v", err)
		}
	}
	if err := owner.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&onShelf); err != nil {
		t.Fatalf("re-read stock: %v", err)
	}
	if onShelf != 0 {
		t.Fatalf("the shelf still holds %d — the fixture did not reach its precondition", onShelf)
	}
}

func TestRetiringTheLastDiscountedVariantIsRefused(t *testing.T) {
	ctx := t.Context()

	var sku string
	if err := pool.QueryRow(ctx, `
		WITH one AS (
			SELECT pv.id, pv.sku, pv.product_id FROM product_variants pv
			WHERE pv.is_active AND pv.compare_at_price_cents IS NOT NULL
			ORDER BY pv.position LIMIT 1
		)
		SELECT sku FROM one`).Scan(&sku); err != nil {
		t.Skipf("the seed has no discounted variant to test with: %v", err)
	}

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE sku = $1`, sku).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET compare_at_price_cents = NULL
		WHERE product_id = $1 AND sku <> $2`, productID, sku); err != nil {
		t.Fatalf("clear siblings: %v", err)
	}

	var campaignID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '測試活動', now() + interval '7 days') RETURNING id`,
		"admin-test-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sale_campaign_products (campaign_id, product_id) VALUES ($1, $2)`,
		campaignID, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM sale_campaigns WHERE id = $1`, campaignID)
	})

	if err := stock.NewStore(pool).SetActive(ctx, sku, false); !errors.Is(err, stock.ErrRefused) {
		t.Errorf("retiring the last discounted variant of a featured product gave %v, "+
			"want ErrRefused — the campaign would point at nothing marked down", err)
	}
}

// TestAReleaseInTheLedgerNamesItsOrder drives a RELEASE, the only movement that reaches
// the order through the reservation: a HOLD stays green with that join deleted.
func TestAReleaseInTheLedgerNamesItsOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	basket := cart.NewStore(pool)

	var vid uuid.UUID
	var sku string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, pv.sku FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock + 2
		ORDER BY pv.stock_quantity DESC LIMIT 1`).Scan(&vid, &sku); err != nil {
		t.Fatalf("find a stocked variant: %v", err)
	}
	number := admintest.PlaceHeldOrder(t, pool, vid)

	if err := basket.CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	view, err := stock.NewStore(pool).Movements(ctx, sku, time.Now())
	if err != nil {
		t.Fatalf("Movements: %v", err)
	}
	var release, hold bool
	for i := range view.Rows {
		m := &view.Rows[i]
		if m.OrderNumber != number {
			continue
		}
		release = release || m.Reason == inventory.ReasonRelease
		hold = hold || m.Reason == inventory.ReasonHold
	}
	if !release {
		seen := make([]string, 0, len(view.Rows))
		for i := range view.Rows {
			seen = append(seen, string(view.Rows[i].Reason)+"/"+view.Rows[i].OrderNumber)
		}
		t.Errorf("no release naming %s in the ledger: %v", number, seen)
	}
	if !hold {
		t.Errorf("no hold naming %s in the ledger", number)
	}
}

// The dashboard's sold-out count and the stock desk's sold-out filter agree: a
// variant that is not for sale, or whose product is not active, is in neither.
func TestAVariantNotForSaleIsNeitherCountedNorListedAsSoldOut(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, _ := admintest.StaffContext(t, owner)
	s := stock.NewStore(owner)
	dashboard := admintest.OrderStore(owner, admintest.Refunder{}, nil, nil)
	read := func() (counted int64, listed int) {
		t.Helper()
		view, err := dashboard.Dashboard(ctx)
		if err != nil {
			t.Fatalf("Dashboard: %v", err)
		}
		soldOut, err := s.Variants(ctx, true, "")
		if err != nil {
			t.Fatalf("Variants(sold out): %v", err)
		}
		return view.SoldOut, len(soldOut.Variants)
	}
	counted, listed := read()

	var variant uuid.UUID
	if err := owner.QueryRow(ctx, `
		SELECT v.id FROM product_variants v JOIN products p ON p.id = v.product_id
		WHERE v.is_active AND p.status = 'active' AND v.stock_quantity > v.safety_stock
		ORDER BY v.id LIMIT 1`).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE product_variants SET safety_stock = stock_quantity WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	if c, l := read(); c != counted+1 || l != listed+1 {
		t.Fatalf("an active variant at its safety stock: counted %d and listed %d, want %d and %d", c, l, counted+1, listed+1)
	}

	if _, err := owner.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	if c, l := read(); c != counted || l != listed {
		t.Errorf("an inactive variant at its safety stock: counted %d and listed %d, want %d and %d", c, l, counted, listed)
	}

	if _, err := owner.Exec(ctx, `UPDATE product_variants SET is_active = true WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "archived"} {
		if _, err := owner.Exec(ctx, `UPDATE products SET status = $2 WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`, variant, status); err != nil {
			t.Fatal(err)
		}
		if c, l := read(); c != counted || l != listed {
			t.Errorf("a %s product's variant at its safety stock: counted %d and listed %d, want %d and %d", status, c, l, counted, listed)
		}
	}
}

func TestRetiringPublishedVariantsThroughTheStockRoute(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	adminPool := admintest.AdminRolePool(t, pool)
	for _, tt := range []struct {
		name          string
		variants      int
		notice        string
		remainsActive bool
		audits        int
	}{
		{name: "last active variant", variants: 1, notice: "refused", remainsActive: true, audits: 0},
		{name: "another active variant", variants: 2, notice: "ok", remainsActive: false, audits: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin fixture: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)
			var productID uuid.UUID
			if err := tx.QueryRow(ctx, `
    INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
    SELECT b.id, c.id, $1, 'Stock retirement test', 'active', now()
    FROM brands b CROSS JOIN categories c LIMIT 1 RETURNING id`,
				"retirement-"+uuid.NewString()).Scan(&productID); err != nil {
				t.Fatalf("create published product: %v", err)
			}
			var targetID uuid.UUID
			sku := "RETIRE-" + strings.ToUpper(uuid.NewString())
			for i := range tt.variants {
				variantSKU := sku
				if i > 0 {
					variantSKU += "-OTHER"
				}
				var id uuid.UUID
				if err := tx.QueryRow(ctx, `
     INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position, is_active)
     VALUES ($1, $2, 10000, 0, $3, true) RETURNING id`, productID, variantSKU, i+1).Scan(&id); err != nil {
					t.Fatalf("create active variant: %v", err)
				}
				if i == 0 {
					targetID = id
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit fixture: %v", err)
			}
			var campaigns, active int
			if err := pool.QueryRow(ctx, `
    SELECT (SELECT count(*) FROM sale_campaign_products WHERE product_id = $1),
           (SELECT count(*) FROM product_variants WHERE product_id = $1 AND is_active)`,
				productID).Scan(&campaigns, &active); err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if campaigns != 0 || active != tt.variants {
				t.Fatalf("fixture campaigns/active variants = %d/%d, want 0/%d", campaigns, active, tt.variants)
			}
			mux := http.NewServeMux()
			handlerOver(stock.NewStore(adminPool)).Routes(mux, admintest.BackOffice)
			form := url.Values{"sku": {sku}, "active": {"0"}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/active", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			wantLocation := "/admin/stock?" + tt.notice + "=1#row-" + sku
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != wantLocation {
				t.Errorf("retirement response = %d %q, want 303 %q", res.Code, res.Header().Get("Location"), wantLocation)
			}
			var remainsActive bool
			var otherActive, audits int
			if err := pool.QueryRow(ctx, `
    SELECT v.is_active,
           (SELECT count(*) FROM product_variants WHERE product_id = $2 AND id <> $1 AND is_active),
           (SELECT count(*) FROM audit_events WHERE entity_id = $1 AND action = 'variant.retire')
    FROM product_variants v WHERE v.id = $1`, targetID, productID).Scan(&remainsActive, &otherActive, &audits); err != nil {
				t.Fatalf("read retirement outcome: %v", err)
			}
			if remainsActive != tt.remainsActive || otherActive != tt.variants-1 || audits != tt.audits {
				t.Errorf("retirement active/other active/audits = %v/%d/%d, want %v/%d/%d", remainsActive, otherActive, audits, tt.remainsActive, tt.variants-1, tt.audits)
			}
		})
	}
	t.Run("closed pool", func(t *testing.T) {
		closed := admintest.NamedPool(t, pool, "retirement-closed-"+uuid.NewString())
		closed.Close()
		mux := http.NewServeMux()
		handlerOver(stock.NewStore(closed)).Routes(mux, admintest.BackOffice)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/active", strings.NewReader("sku=RETIRE-CLOSED&active=0"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
			t.Errorf("closed pool response = %d %q, want 500 without Location", res.Code, res.Header().Get("Location"))
		}
	})
}
