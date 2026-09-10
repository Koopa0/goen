//go:build integration

package warranty_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/warranty"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	p, stop, err := dbtest.Start(ctx)
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	// The seed supplies shipping_method_versions; the migration creates none.
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		panic(err)
	}
	if _, seedErr := pool.Exec(ctx, string(seed)); seedErr != nil {
		panic(seedErr)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

type parcel struct {
	units       int
	arrived     bool
	deliveredAt time.Time
}

// fixture is one customer's order: `ordered` units bought, one parcel carrying
// `p.units` of them, covered for `months` — 0 meaning no term was set.
type fixture struct {
	userID    string
	number    string
	lineID    uuid.UUID
	variantID uuid.UUID
	productID uuid.UUID
}

const warrantyFixtureNote = "Original coverage promise"

func newFixture(t *testing.T, ordered, months int, p parcel) fixture {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('w-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固測試')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A product and a variant of its own, so a case that changes the term does
	// not change it for every other case.
	var variantID, productID uuid.UUID
	var term any
	if months > 0 {
		term = months
	}
	if err := tx.QueryRow(ctx, `
		WITH b AS (
			INSERT INTO brands (slug, name) VALUES ('wb-' || gen_random_uuid(), '保固品牌')
			RETURNING id
		), c AS (
			INSERT INTO categories (slug, name, position) SELECT 'wc-' || gen_random_uuid(), '保固分類', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL
			RETURNING id
		), p AS (
			INSERT INTO products (
				brand_id, category_id, slug, name, warranty_months, warranty_note
			)
			SELECT b.id, c.id, 'w-' || gen_random_uuid(), '保固測試商品', $1::integer,
			       CASE WHEN $1::integer IS NULL THEN NULL ELSE $2::text END
			FROM b, c
			RETURNING id
		)
		INSERT INTO product_variants (product_id, sku, price_cents)
		-- product_variants_sku_format allows only upper-case alphanumerics.
		SELECT p.id, 'W-' || upper(replace(gen_random_uuid()::text, '-', '')), 100000 FROM p
		RETURNING id, product_id`, term, warrantyFixtureNote).Scan(&variantID, &productID); err != nil {
		t.Fatalf("create product: %v", err)
	}

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         warranty_note, warranty_months,
		                         unit_price_cents, quantity)
		SELECT $1, pv.id, 'W-SKU', '保固測試商品',
		       pr.warranty_note, pr.warranty_months, 100000, $3
		FROM product_variants pv
		JOIN products pr ON pr.id = pv.product_id
		WHERE pv.id = $2
		RETURNING id`,
		orderID, variantID, ordered).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}

	// order_has_delivery_details is DEFERRED and fires at commit.
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'w@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create delivery details: %v", err)
	}

	if p.units > 0 {
		addWarrantyShipment(t, tx, orderID, lineID, number, ordered, p)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return fixture{
		userID: userID.String(), number: number, lineID: lineID,
		variantID: variantID, productID: productID,
	}
}

func addWarrantyShipment(
	t *testing.T,
	tx pgx.Tx,
	orderID, lineID uuid.UUID,
	number string,
	ordered int,
	p parcel,
) {
	t.Helper()
	ctx := t.Context()
	ref := "cs_warranty_" + number
	amount := int64(ordered) * 100000
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, ref, amount); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, ref, amount); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	for _, status := range []string{"picking", "shipped"} {
		if _, err := tx.Exec(ctx,
			`UPDATE orders SET fulfillment_status = $2 WHERE id = $1`, orderID, status); err != nil {
			t.Fatalf("move order to %s: %v", status, err)
		}
	}

	shippedAt, deliveredAt := warrantyParcelTimes(p)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, '黑貓', 'TW-'||$2, $3, $4)
		RETURNING id`,
		orderID, number, shippedAt, deliveredAt).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, p.units); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
}

// warrantyParcelTimes keeps dispatch and delivery two days apart so the test
// fixture cannot pass whichever source timestamp the expiry query happens to use.
func warrantyParcelTimes(p parcel) (shippedAt time.Time, deliveredAt any) {
	now := time.Now()
	shippedAt = now.Add(-10 * 24 * time.Hour)
	if !p.arrived {
		return shippedAt, nil
	}
	delivery := now.Add(-8 * 24 * time.Hour)
	if !p.deliveredAt.IsZero() {
		delivery = p.deliveredAt
		shippedAt = delivery.Add(-48 * time.Hour)
	}
	return shippedAt, delivery
}

// TestRegistrationIsBoundedByWhatArrived proves cover cannot start before the
// goods arrive.
func TestRegistrationIsBoundedByWhatArrived(t *testing.T) {
	s := warranty.NewStore(pool)

	tests := []struct {
		name    string
		ordered int
		p       parcel
		unit    int
		wantOK  bool
	}{
		{"nothing dispatched", 3, parcel{units: 0}, 1, false},
		{"dispatched but still in transit", 3, parcel{units: 3, arrived: false}, 1, false},
		{"first of one delivered", 3, parcel{units: 1, arrived: true}, 1, true},
		{"second when only one delivered", 3, parcel{units: 1, arrived: true}, 2, false},
		{"all delivered", 2, parcel{units: 2, arrived: true}, 2, true},
		{"beyond what was ordered", 2, parcel{units: 2, arrived: true}, 3, false},
		{"zero is not a unit", 2, parcel{units: 2, arrived: true}, 0, false},
		{"negative is not a unit", 2, parcel{units: 2, arrived: true}, -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.ordered, 24, tt.p)
			err := s.Register(t.Context(), f.lineID.String(), f.userID, "", tt.unit)
			if (err == nil) != tt.wantOK {
				t.Errorf("register unit %d of a parcel of %d (arrived=%v): err=%v, want ok=%v",
					tt.unit, tt.p.units, tt.p.arrived, err, tt.wantOK)
			}
		})
	}
}

// TestTheFormOffersNothingWhileTheParcelIsInTransit is the READ side of that
// boundary.
func TestTheFormOffersNothingWhileTheParcelIsInTransit(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)

	transit := newFixture(t, 1, 12, parcel{units: 1, arrived: false})
	view, err := s.Registrable(ctx, transit.number, transit.userID)
	if err != nil {
		t.Fatalf("read registrable lines: %v", err)
	}
	if view.AnyRegistrable() {
		t.Error("the form offered a unit that is still in transit")
	}
	if got := view.Lines[0].Delivered; got != 0 {
		t.Errorf("a parcel in transit counted %d delivered units, want 0", got)
	}

	// The control: without it, a query returning nothing for every order passes.
	arrived := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	view, err = s.Registrable(ctx, arrived.number, arrived.userID)
	if err != nil {
		t.Fatalf("read registrable lines: %v", err)
	}
	if !view.AnyRegistrable() {
		t.Error("a delivered unit was not offered for registration")
	}
}

// TestAProductWithNoTermCannotBeRegistered proves goen never invents a term.
func TestAProductWithNoTermCannotBeRegistered(t *testing.T) {
	s := warranty.NewStore(pool)

	noTerm := newFixture(t, 1, 0, parcel{units: 1, arrived: true})
	err := s.Register(t.Context(), noTerm.lineID.String(), noTerm.userID, "", 1)
	// ErrNotRegistrable specifically: a missing term also trips expires_on's NOT
	// NULL further down, so "an error happened" stays green with the guard gone.
	if !errors.Is(err, warranty.ErrNotRegistrable) {
		t.Errorf("a product with no stated term gave %v, want ErrNotRegistrable — "+
			"either goen invented a warranty, or it refused one by crashing", err)
	}

	withTerm := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(t.Context(), withTerm.lineID.String(), withTerm.userID, "", 1); err != nil {
		t.Errorf("a product with a term was refused: %v", err)
	}
}

// TestTheExpiryRunsFromDeliveryAndNotFromDispatch compares DATES, not a month
// count: two days do not add a month, so a month-count assertion is blind here.
func TestTheExpiryRunsFromDeliveryAndNotFromDispatch(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	deliveredAt, err := time.Parse(time.RFC3339, "2026-08-25T07:00:00+08:00")
	if err != nil {
		t.Fatalf("parse delivered_at: %v", err)
	}
	f := newFixture(t, 1, 24, parcel{
		units: 1, arrived: true, deliveredAt: deliveredAt,
	})

	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 1); err != nil {
		t.Fatalf("register: %v", err)
	}

	var fromShopDelivery, fromShopDispatch, fromAmbientDelivery bool
	if err := pool.QueryRow(ctx, `
		SELECT w.expires_on = (shop_day(s.delivered_at) + interval '24 months')::date,
		       w.expires_on = (shop_day(s.shipped_at)   + interval '24 months')::date,
		       w.expires_on = (s.delivered_at::date     + interval '24 months')::date
		FROM warranty_registrations w
		JOIN order_shipment_lines sl ON sl.order_line_id = w.order_line_id
		JOIN order_shipments s ON s.id = sl.shipment_id
		WHERE w.order_line_id = $1`, f.lineID).Scan(
		&fromShopDelivery, &fromShopDispatch, &fromAmbientDelivery,
	); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !fromShopDelivery {
		t.Error("the cover does not run 24 months from the day the parcel arrived")
	}
	if fromShopDispatch {
		t.Error("the cover runs from the DISPATCH date, which is short by the time " +
			"in transit — and the days lost come off the customer")
	}
	if fromAmbientDelivery {
		t.Error("the cover runs from delivered_at::date in the ambient session zone; " +
			"07:00 Taipei is the previous day in UTC")
	}
}

// TestThePurchasedPromiseSurvivesCatalogueRetirement proves an order owns the
// warranty bought: a later catalogue edit or retirement may not rewrite it.
func TestThePurchasedPromiseSurvivesCatalogueRetirement(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	f := newFixture(t, 2, 24, parcel{units: 2, arrived: true})
	var productSlug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM products WHERE id = $1`, f.productID).
		Scan(&productSlug); err != nil {
		t.Fatalf("read product slug: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE products
		SET warranty_months = 1, warranty_note = 'Replacement live promise'
		WHERE id = $1`, f.productID); err != nil {
		t.Fatalf("edit live warranty: %v", err)
	}

	assertPurchasedPromise := func(stage string) {
		t.Helper()
		view, err := s.Registrable(ctx, f.number, f.userID)
		if err != nil {
			t.Fatalf("%s: read registrable line: %v", stage, err)
		}
		if len(view.Lines) != 1 {
			t.Fatalf("%s: got %d lines, want 1", stage, len(view.Lines))
		}
		line := view.Lines[0]
		if !line.HasTerm || line.Months != 24 {
			t.Errorf("%s: warranty term = %d months (present=%v), want purchased 24",
				stage, line.Months, line.HasTerm)
		}
		if line.Note != warrantyFixtureNote {
			t.Errorf("%s: warranty note = %q, want purchased %q",
				stage, line.Note, warrantyFixtureNote)
		}
	}
	assertPurchasedPromise("after live edit")

	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET is_active = false WHERE id = $1`, f.variantID); err != nil {
		t.Fatalf("retire live variant: %v", err)
	}
	assertPurchasedPromise("after variant retirement")
	registrable, err := s.Registrable(ctx, f.number, f.userID)
	if err != nil {
		t.Fatalf("read line after variant retirement: %v", err)
	}
	if registrable.Lines[0].Slug != productSlug {
		t.Errorf("variant retirement changed product link to %q, want %q",
			registrable.Lines[0].Slug, productSlug)
	}
	if registerErr := s.Register(ctx, f.lineID.String(), f.userID, "", 1); registerErr != nil {
		t.Fatalf("register after variant retirement: %v", registerErr)
	}
	registered, err := s.Mine(ctx, f.userID)
	if err != nil {
		t.Fatalf("list after variant retirement: %v", err)
	}
	if len(registered) != 1 || registered[0].Slug != productSlug {
		t.Fatalf("registered warranty product link = %+v, want slug %q", registered, productSlug)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'archived' WHERE id = $1`, f.productID); err != nil {
		t.Fatalf("archive live product: %v", err)
	}
	assertPurchasedPromise("after catalogue retirement")

	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 2); err != nil {
		t.Fatalf("register after catalogue retirement: %v", err)
	}
	var keptOriginalTerm bool
	if err := pool.QueryRow(ctx, `
		SELECT bool_and(w.expires_on =
		       (shop_day(s.delivered_at) + make_interval(months => 24))::date)
		FROM warranty_registrations w
		JOIN order_shipment_lines sl ON sl.order_line_id = w.order_line_id
		JOIN order_shipments s ON s.id = sl.shipment_id
		WHERE w.order_line_id = $1`, f.lineID).Scan(&keptOriginalTerm); err != nil {
		t.Fatalf("read snapshotted expiry: %v", err)
	}
	if !keptOriginalTerm {
		t.Error("registration after catalogue retirement did not use the purchased 24-month term")
	}
}

// TestOnlyTheOwnerCanRegisterOrSee proves the scope is a WHERE clause rather
// than a check in Go, by posting straight to the store.
func TestOnlyTheOwnerCanRegisterOrSee(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	mine := newFixture(t, 2, 12, parcel{units: 2, arrived: true})
	theirs := newFixture(t, 1, 12, parcel{units: 1, arrived: true})

	if err := s.Register(ctx, mine.lineID.String(), theirs.userID, "", 1); err == nil {
		t.Error("somebody registered a warranty against another customer's order")
	}
	if _, err := s.Registrable(ctx, mine.number, theirs.userID); !errors.Is(err, warranty.ErrNotFound) {
		t.Errorf("another customer's order read as %v, want ErrNotFound", err)
	}
	// An order that does not exist gives the SAME answer.
	if _, err := s.Registrable(ctx, "GOEN-NOPE-0001", mine.userID); !errors.Is(err, warranty.ErrNotFound) {
		t.Errorf("an absent order read as %v, want ErrNotFound", err)
	}

	if err := s.Register(ctx, mine.lineID.String(), mine.userID, "", 1); err != nil {
		t.Errorf("the owner was refused: %v", err)
	}
}

// TestAUnitIsRegisteredOnce proves a unit gets cover once and its neighbours
// still can.
func TestAUnitIsRegisteredOnce(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	f := newFixture(t, 2, 12, parcel{units: 2, arrived: true})

	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 1); err == nil {
		t.Error("the same unit was registered twice")
	}
	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 2); err != nil {
		t.Errorf("the second unit was refused: %v", err)
	}
}

// TestASerialNumberIsRegisteredOnceAcrossTheWholeShop proves a mistyped serial
// is caught, and an absent one is not a duplicate.
func TestASerialNumberIsRegisteredOnceAcrossTheWholeShop(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	a := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	b := newFixture(t, 1, 12, parcel{units: 1, arrived: true})

	const serial = "SN-SHARED-0001"
	if err := s.Register(ctx, a.lineID.String(), a.userID, serial, 1); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.Register(ctx, b.lineID.String(), b.userID, serial, 1)
	if !errors.Is(err, warranty.ErrSerialTaken) {
		t.Errorf("a duplicate serial gave %v, want ErrSerialTaken", err)
	}

	// An EMPTY serial is not a duplicate of another: the unique index is partial.
	if err := s.Register(ctx, b.lineID.String(), b.userID, "", 1); err != nil {
		t.Errorf("a registration with no serial was refused: %v", err)
	}
	c := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(ctx, c.lineID.String(), c.userID, "", 1); err != nil {
		t.Errorf("a second registration with no serial was refused: %v", err)
	}
}

// TestMineShowsOnlyThisCustomersCover proves the list is scoped to its owner.
func TestMineShowsOnlyThisCustomersCover(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	mine := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	theirs := newFixture(t, 1, 12, parcel{units: 1, arrived: true})

	if err := s.Register(ctx, mine.lineID.String(), mine.userID, "", 1); err != nil {
		t.Fatalf("register mine: %v", err)
	}
	if err := s.Register(ctx, theirs.lineID.String(), theirs.userID, "", 1); err != nil {
		t.Fatalf("register theirs: %v", err)
	}

	rows, err := s.Mine(ctx, mine.userID)
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1 — the list is not scoped to its owner", len(rows))
	}
	if rows[0].Order != mine.number {
		t.Errorf("the row names order %s, want %s", rows[0].Order, mine.number)
	}
	if !rows[0].InForce {
		t.Error("cover registered today reads as expired")
	}
}
