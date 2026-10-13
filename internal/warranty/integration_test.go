//go:build integration

package warranty_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/user"
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
	return newFixtureForCustomer(t, uuid.Nil, ordered, months, p)
}

func newFixtureForCustomer(t *testing.T, userID uuid.UUID, ordered, months int, p parcel) fixture {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)

	if userID == uuid.Nil {
		if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('w-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固測試')
		RETURNING id`).Scan(&userID); err != nil {
			t.Fatalf("create user: %v", err)
		}
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
		SELECT $1, pv.id, pv.sku, pr.name,
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
		VALUES ($1, 'black_cat', 'TW-'||$2, $3, $4)
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
			err := s.Register(t.Context(), f.number, f.lineID.String(), f.userID, "", tt.unit)
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
	err := s.Register(t.Context(), noTerm.number, noTerm.lineID.String(), noTerm.userID, "", 1)
	// ErrNotRegistrable specifically: a missing term also trips expires_on's NOT
	// NULL further down, so "an error happened" stays green with the guard gone.
	if !errors.Is(err, warranty.ErrNotRegistrable) {
		t.Errorf("a product with no stated term gave %v, want ErrNotRegistrable — "+
			"either goen invented a warranty, or it refused one by crashing", err)
	}

	withTerm := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(t.Context(), withTerm.number, withTerm.lineID.String(), withTerm.userID, "", 1); err != nil {
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

	if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); err != nil {
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
	if registerErr := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); registerErr != nil {
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

	if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 2); err != nil {
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

	if err := s.Register(ctx, mine.number, mine.lineID.String(), theirs.userID, "", 1); err == nil {
		t.Error("somebody registered a warranty against another customer's order")
	}
	if _, err := s.Registrable(ctx, mine.number, theirs.userID); !errors.Is(err, warranty.ErrNotFound) {
		t.Errorf("another customer's order read as %v, want ErrNotFound", err)
	}
	// An order that does not exist gives the SAME answer.
	if _, err := s.Registrable(ctx, "GOEN-NOPE-0001", mine.userID); !errors.Is(err, warranty.ErrNotFound) {
		t.Errorf("an absent order read as %v, want ErrNotFound", err)
	}

	if err := s.Register(ctx, mine.number, mine.lineID.String(), mine.userID, "", 1); err != nil {
		t.Errorf("the owner was refused: %v", err)
	}
}

// TestAUnitIsRegisteredOnce proves a unit gets cover once and its neighbours
// still can.
func TestAUnitIsRegisteredOnce(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	f := newFixture(t, 2, 12, parcel{units: 2, arrived: true})

	if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); err == nil {
		t.Error("the same unit was registered twice")
	}
	if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 2); err != nil {
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
	if err := s.Register(ctx, a.number, a.lineID.String(), a.userID, serial, 1); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.Register(ctx, b.number, b.lineID.String(), b.userID, serial, 1)
	if !errors.Is(err, warranty.ErrSerialTaken) {
		t.Errorf("a duplicate serial gave %v, want ErrSerialTaken", err)
	}

	// An EMPTY serial is not a duplicate of another: the unique index is partial.
	if err := s.Register(ctx, b.number, b.lineID.String(), b.userID, "", 1); err != nil {
		t.Errorf("a registration with no serial was refused: %v", err)
	}
	c := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(ctx, c.number, c.lineID.String(), c.userID, "", 1); err != nil {
		t.Errorf("a second registration with no serial was refused: %v", err)
	}
}

// TestMineShowsOnlyThisCustomersCover proves the list is scoped to its owner.
func TestMineShowsOnlyThisCustomersCover(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	mine := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	theirs := newFixture(t, 1, 12, parcel{units: 1, arrived: true})

	if err := s.Register(ctx, mine.number, mine.lineID.String(), mine.userID, "", 1); err != nil {
		t.Fatalf("register mine: %v", err)
	}
	if err := s.Register(ctx, theirs.number, theirs.lineID.String(), theirs.userID, "", 1); err != nil {
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

// TestMineStatesNoCoverForAnOrderReturnedInFull holds that cover registered before a return is not listed as in
// force once every unit of the order is in a return whose refund has settled, and that a part of the order in one,
// or an approved return whose card refund has not settled, leaves it.
func TestMineStatesNoCoverForAnOrderReturnedInFull(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)

	for _, tc := range []struct {
		name     string
		ordered  int
		returned int
		refunded bool // the payout wrote the refunded event
		want     bool
	}{
		{"every unit, refunded", 1, 1, true, true},
		{"every unit, the card refund not yet settled", 1, 1, false, false},
		{"one unit of two, refunded", 2, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.ordered, 12, parcel{units: tc.ordered, arrived: true})
			if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); err != nil {
				t.Fatalf("register: %v", err)
			}
			var orderID, returnID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT order_id FROM order_lines WHERE id = $1`, f.lineID).Scan(&orderID); err != nil {
				t.Fatalf("read order: %v", err)
			}
			if err := pool.QueryRow(ctx, `INSERT INTO return_requests (order_id, reason) VALUES ($1, '') RETURNING id`,
				orderID).Scan(&returnID); err != nil {
				t.Fatalf("return request: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, $4)`, orderID, returnID, f.lineID, tc.returned); err != nil {
				t.Fatalf("return line: %v", err)
			}
			// The approval freezes a card refund of the returned units against the captured payment.
			if _, err := pool.Exec(ctx, `
				UPDATE return_requests SET status = 'approved', decided_at = now() WHERE id = $1`, returnID); err != nil {
				t.Fatalf("approve return: %v", err)
			}
			if tc.refunded {
				if _, err := pool.Exec(ctx, `
					INSERT INTO order_events (order_id, kind, return_request_id) VALUES ($1, 'refunded', $2)`,
					orderID, returnID); err != nil {
					t.Fatalf("record the refund: %v", err)
				}
			}

			rows, err := s.Mine(ctx, f.userID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("Mine = %d rows, err %v; want 1", len(rows), err)
			}
			if rows[0].Returned != tc.want || rows[0].InForce == tc.want {
				t.Errorf("Returned = %v, InForce = %v; want Returned %v and InForce %v",
					rows[0].Returned, rows[0].InForce, tc.want, !tc.want)
			}
		})
	}
}

// TestEachUnitsCoverStartsWhenItsOwnParcelArrived: with the line split across
// two parcels, unit 2 is in the second box and must not take the first box's
// earlier date.
func TestEachUnitsCoverStartsWhenItsOwnParcelArrived(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	first, err := time.Parse(time.RFC3339, "2026-08-10T12:00:00+08:00")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	second := first.Add(9 * 24 * time.Hour)
	f := newFixture(t, 2, 12, parcel{units: 1, arrived: true, deliveredAt: first})

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT order_id FROM order_lines WHERE id = $1`, f.lineID).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	var shipmentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, 'black_cat', 'TW2-'||$2, $3, $4) RETURNING id`,
		orderID, f.number, second.Add(-48*time.Hour), second).Scan(&shipmentID); err != nil {
		t.Fatalf("second shipment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, f.lineID); err != nil {
		t.Fatalf("second shipment line: %v", err)
	}

	for unit := 1; unit <= 2; unit++ {
		if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", unit); err != nil {
			t.Fatalf("register unit %d: %v", unit, err)
		}
	}
	for unit, arrived := range map[int]time.Time{1: first, 2: second} {
		var ok bool
		if err := pool.QueryRow(ctx, `
			SELECT expires_on = (shop_day($3::timestamptz) + interval '12 months')::date
			FROM warranty_registrations WHERE order_line_id = $1 AND unit_no = $2`,
			f.lineID, unit, arrived).Scan(&ok); err != nil {
			t.Fatalf("read unit %d: %v", unit, err)
		}
		if !ok {
			t.Errorf("unit %d's cover does not run from the day its own parcel arrived (%s)",
				unit, arrived.Format(time.DateOnly))
		}
	}
}

// TestOnlyDecidedReturnsTakeUnitsOffWhatCanBeRegistered: a returned unit can no
// longer start cover once its return is approved or completed. An open or
// rejected return takes nothing, and a registration already made is left alone.
func TestOnlyDecidedReturnsTakeUnitsOffWhatCanBeRegistered(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)

	for _, tc := range []struct {
		status    string
		decide    []string
		wantUnits int
	}{
		{"requested", nil, 2},
		{"rejected", []string{`UPDATE return_requests SET status = 'rejected', decided_at = now() WHERE id = $1`}, 2},
		{"approved", []string{approveReturnSQL}, 1},
		{"completed", []string{
			approveReturnSQL,
			`INSERT INTO refunds (payment_id, return_request_id, request_key, provider_ref, status,
			                     amount_cents, succeeded_at)
			 SELECT p.id, rr.id, 'warranty-refund-' || rr.id, 're_' || replace(rr.id::text, '-', ''),
			        'succeeded', 100000, now()
			 FROM return_requests rr JOIN payments p ON p.order_id = rr.order_id
			 WHERE rr.id = $1`,
			`INSERT INTO order_events (order_id, kind, return_request_id)
			 SELECT order_id, 'refunded', id FROM return_requests WHERE id = $1`,
			`UPDATE return_request_lines SET received_quantity = 1, restocked_quantity = 1 WHERE return_request_id = $1`,
			`UPDATE return_requests SET status = 'completed' WHERE id = $1`,
		}, 1},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f := newFixture(t, 2, 12, parcel{units: 2, arrived: true})
			var orderID, returnID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT order_id FROM order_lines WHERE id = $1`, f.lineID).Scan(&orderID); err != nil {
				t.Fatalf("read order: %v", err)
			}
			if err := pool.QueryRow(ctx, `
				INSERT INTO return_requests (order_id, reason) VALUES ($1, '') RETURNING id`,
				orderID).Scan(&returnID); err != nil {
				t.Fatalf("return request: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
				VALUES ($1, $2, $3, 1)`, orderID, returnID, f.lineID); err != nil {
				t.Fatalf("return line: %v", err)
			}
			for _, stmt := range tc.decide {
				if _, err := pool.Exec(ctx, stmt, returnID); err != nil {
					t.Fatalf("move return to %s: %v", tc.status, err)
				}
			}

			view, err := s.Registrable(ctx, f.number, f.userID)
			if err != nil {
				t.Fatalf("registrable: %v", err)
			}
			if got := view.Lines[0].Delivered; got != tc.wantUnits {
				t.Errorf("a %s return leaves %d registrable units of 2, want %d", tc.status, got, tc.wantUnits)
			}
			second := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 2)
			if (second == nil) != (tc.wantUnits == 2) {
				t.Errorf("registering unit 2 under a %s return gave %v, want ok=%v", tc.status, second, tc.wantUnits == 2)
			}
			if err := s.Register(ctx, f.number, f.lineID.String(), f.userID, "", 1); err != nil {
				t.Errorf("the one unit not returned was refused: %v", err)
			}
		})
	}
}

const approveReturnSQL = `
	UPDATE return_requests
	SET status = 'approved', decided_at = now(), goods_refund_cents = 100000,
	    card_refund_cents = 100000, credit_refund_cents = 0
	WHERE id = $1`

func TestWarrantySerialRefusalsRetainOnlyTheAuthorizedLine(t *testing.T) {
	app := warrantyStoreRolePool(t)
	s := warranty.NewStore(app)
	h := warranty.NewHandler(s, slog.Default())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/warranty/{number}", h.Register)
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name   string
			serial string
			unit   string
			target string
			status int
			field  i18n.Key
		}{
			{name: "duplicate serial", unit: "1", status: 422, field: i18n.KeyWarrantyDuplicateSerial},
			{name: "long ascii", serial: "  " + strings.Repeat("A", 61) + "  ", unit: "1", status: 422, field: i18n.KeyWarrantySerialTooLong},
			{name: "long unicode", serial: "  " + strings.Repeat("界", 61) + "  ", unit: "1", status: 422, field: i18n.KeyWarrantySerialTooLong},
			{name: "invalid unit", serial: " Keep & \"draft\" ", unit: "invalid", status: 422},
			{name: "foreign line", serial: " Never reflect foreign draft ", unit: "1", target: "foreign line", status: 422},
			{name: "foreign order", serial: " Never reflect foreign draft ", unit: "1", target: "foreign order", status: 404},
		} {
			t.Run(locale.Tag()+"/"+tc.name, func(t *testing.T) {
				mine := newFixture(t, 2, 12, parcel{units: 2, arrived: true})
				other := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
				serial := "duplicate-" + uuid.NewString()
				if err := s.Register(t.Context(), other.number, other.lineID.String(), other.userID, serial, 1); err != nil {
					t.Fatalf("register duplicate control: %v", err)
				}
				draft := tc.serial
				if tc.name == "duplicate serial" {
					draft = "  " + serial + "  "
				}
				line, number := mine.lineID.String(), mine.number
				if tc.target == "foreign line" {
					line = other.lineID.String()
				}
				if tc.target == "foreign order" {
					line, number = other.lineID.String(), other.number
				}
				before := warrantyRegistrationState(t, mine.lineID, other.lineID)
				ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: mine.userID, Role: user.RoleCustomer})
				form := url.Values{"line": {line}, "unit": {tc.unit}, "serial": {draft}}
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+number, strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("warranty refusal status=%d, want %d", w.Code, tc.status)
				}
				if got := warrantyRegistrationState(t, mine.lineID, other.lineID); got != before {
					t.Errorf("refused registration changed saved rows: %s, want %s", got, before)
				}
				nodes := warrantyResponseNodes(t, w.Body.String())
				if nodes["serial-"+other.lineID.String()] != nil {
					t.Error("refusal included an unauthorized order line")
				}
				if tc.target != "" {
					if strings.Contains(w.Body.String(), strings.TrimSpace(draft)) {
						t.Error("foreign draft reached the response")
					}
					input := warrantyResponseAttrs(nodes["serial-"+mine.lineID.String()])
					if input["value"] != "" || input["aria-invalid"] != "" {
						t.Errorf("foreign draft marked the authorized input: %v", input)
					}
					return
				}
				inputID := "serial-" + mine.lineID.String()
				input := warrantyResponseAttrs(nodes[inputID])
				if input["value"] != draft {
					t.Errorf("retained serial=%q, want %q", input["value"], draft)
				}
				if tc.field == "" {
					alert := nodes["warranty-refusal"]
					if input["aria-invalid"] != "" || alert == nil || warrantyResponseAttrs(alert)["role"] != "alert" || warrantyResponseText(alert) != i18n.T(ctx, i18n.KeyWarrantyRefused) {
						t.Errorf("unit refusal must keep its draft with a form alert, not blame the serial: %v", input)
					}
					return
				}
				message := warrantyResponseText(nodes[inputID+"-error"])
				want := i18n.T(ctx, tc.field)
				if tc.field == i18n.KeyWarrantySerialTooLong {
					want = fmt.Sprintf(want, warranty.MaxSerialRunes)
					if !strings.Contains(message, strconv.Itoa(warranty.MaxSerialRunes)) || strings.Contains(message, "%!") {
						t.Errorf("serial length refusal = %q, want the accepted limit without a formatting error", message)
					}
				}
				if input["aria-invalid"] != "true" || input["aria-describedby"] != "serial-hint-"+mine.lineID.String()+" "+inputID+"-error" || message != want {
					t.Errorf("refused serial has no associated localized explanation: %v, message=%q, want %q", input, message, want)
				}
			})
		}
	}
}

func warrantyRegistrationState(t *testing.T, lines ...uuid.UUID) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(to_jsonb(w) ORDER BY w.id), '[]'::jsonb)::text FROM warranty_registrations w WHERE order_line_id = ANY($1::uuid[])`, lines).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func warrantyResponseNodes(t *testing.T, body string) map[string]*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]*html.Node)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "id" {
				if nodes[a.Val] != nil {
					t.Errorf("duplicate ID %q", a.Val)
				}
				nodes[a.Val] = n
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return nodes
}

func warrantyResponseAttrs(n *html.Node) map[string]string {
	attrs := make(map[string]string)
	if n != nil {
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
	}
	return attrs
}

func warrantyResponseText(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(warrantyResponseText(child))
	}
	return b.String()
}

// TestAFullyReturnedLineIsNotWaitingOnDelivery: the goods arrived and came
// back, so the page must say so rather than promise registration on delivery.
func TestAFullyReturnedLineIsNotWaitingOnDelivery(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	s := warranty.NewStore(pool)
	f := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	var orderID, returnID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT order_id FROM order_lines WHERE id = $1`, f.lineID).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO return_requests (order_id, reason) VALUES ($1, '') RETURNING id`,
		orderID).Scan(&returnID); err != nil {
		t.Fatalf("return request: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, returnID, f.lineID); err != nil {
		t.Fatalf("return line: %v", err)
	}
	if _, err := pool.Exec(ctx, // A zero-amount refund needs no provider payment to settle.
		`UPDATE return_requests SET status = 'approved', decided_at = now(), goods_refund_cents = 0,
			 card_refund_cents = 0, credit_refund_cents = 0 WHERE id = $1`, returnID); err != nil {
		t.Fatalf("approve return: %v", err)
	}

	view, err := s.Registrable(ctx, f.number, f.userID)
	if err != nil {
		t.Fatalf("registrable: %v", err)
	}
	if got := view.Lines[0].Why(ctx); got != i18n.T(ctx, i18n.KeyWarrantyReturned) {
		t.Errorf("a fully returned line says %q, want the returned sentence", got)
	}
	if got := view.EmptyHint(ctx); got == i18n.T(ctx, i18n.KeyWarrantyAfterShipping) {
		t.Errorf("the page promises registration on delivery for goods already returned: %q", got)
	}
}

func warrantyStoreRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config := pool.Config().Copy()
	config.MaxConns = 2
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	app, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	var role string
	if err := app.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("warranty role=%q, error=%v, want store", role, err)
	}
	return app
}

func TestWarrantySerialLengthIsEnforcedForTheStoreRole(t *testing.T) {
	app := warrantyStoreRolePool(t)
	uuidRunes := len(uuid.NewString())
	for _, tc := range []struct {
		name    string
		serial  any
		refused bool
	}{
		{name: "optional serial", serial: nil},
		{name: "ascii boundary", serial: uuid.NewString() + strings.Repeat("A", warranty.MaxSerialRunes-uuidRunes)},
		{name: "unicode boundary", serial: uuid.NewString() + strings.Repeat("界", warranty.MaxSerialRunes-uuidRunes)},
		{name: "ascii overlong", serial: uuid.NewString() + strings.Repeat("A", warranty.MaxSerialRunes-uuidRunes+1), refused: true},
		{name: "unicode overlong", serial: uuid.NewString() + strings.Repeat("界", warranty.MaxSerialRunes-uuidRunes+1), refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
			// A direct write bypasses Register's Go validation while retaining the production role and unit constraints.
			_, err := app.Exec(ctx, `
				INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
				VALUES ($1, 1, $2, $3, DATE '2030-01-01')`, f.lineID, f.userID, tc.serial)
			if tc.refused {
				pgErr, ok := errors.AsType[*pgconn.PgError](err)
				if !ok || pgErr.Code != "23514" || pgErr.ConstraintName != "warranty_registrations_serial_length" {
					t.Errorf("store-role insert with %s serial = %v, want SQLSTATE 23514 constraint warranty_registrations_serial_length", tc.name, err)
				}
				var count int
				if readErr := pool.QueryRow(ctx, `SELECT count(*) FROM warranty_registrations WHERE order_line_id = $1`, f.lineID).Scan(&count); readErr != nil {
					t.Fatal(readErr)
				}
				if count != 0 {
					t.Errorf("refused serial persisted %d registrations, want 0", count)
				}
				return
			}
			if err != nil {
				t.Fatalf("store-role insert with %s serial = %v, want success", tc.name, err)
			}
			var saved *string
			if err := pool.QueryRow(ctx, `SELECT serial_number FROM warranty_registrations WHERE order_line_id = $1`, f.lineID).Scan(&saved); err != nil {
				t.Fatal(err)
			}
			if tc.serial == nil {
				if saved != nil {
					t.Errorf("optional serial = %q, want NULL", *saved)
				}
			} else if saved == nil || *saved != tc.serial {
				t.Errorf("saved serial = %v, want %q", saved, tc.serial)
			}
		})
	}
}

func TestWarrantyRegistrationSuccessStillRedirects(t *testing.T) {
	app := warrantyStoreRolePool(t)
	h := warranty.NewHandler(warranty.NewStore(app), slog.Default())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/warranty/{number}", h.Register)
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct{ name, serial string }{
			{name: "optional blank"},
			{name: "ascii boundary", serial: "  " + strings.Repeat("A", 60) + "  "},
			{name: "unicode boundary", serial: "  " + strings.Repeat("界", 60) + "  "},
		} {
			t.Run(locale.Tag()+"/"+tc.name, func(t *testing.T) {
				f := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
				ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: f.userID, Role: user.RoleCustomer})
				// Boundary serials must remain unique across the shop.
				serial := tc.serial
				if serial != "" {
					serial = "  " + uuid.NewString() + string([]rune(strings.TrimSpace(serial))[36:]) + "  "
				}
				form := url.Values{"line": {f.lineID.String()}, "unit": {"1"}, "serial": {serial}}
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+f.number, strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/account/warranty/"+f.number+"?ok=1" {
					t.Fatalf("successful warranty status=%d, location=%q; want 303 to its order", w.Code, w.Header().Get("Location"))
				}
				var count int
				var saved string
				if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(min(serial_number), '') FROM warranty_registrations WHERE order_line_id = $1`, f.lineID).Scan(&count, &saved); err != nil {
					t.Fatal(err)
				}
				if count != 1 || saved != strings.TrimSpace(serial) {
					t.Errorf("registered rows=%d, serial=%q; want one trimmed serial %q", count, saved, strings.TrimSpace(serial))
				}
			})
		}
	}
}
