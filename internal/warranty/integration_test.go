//go:build integration

package warranty_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/koopa0/goen/internal/warranty"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("goen"),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		panic(err)
	}
	dsn, dsnErr := container.ConnectionString(ctx, "sslmode=disable")
	if dsnErr != nil {
		panic(dsnErr)
	}
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		panic(err)
	}
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	if _, schemaErr := pool.Exec(ctx, string(schema)); schemaErr != nil {
		panic(schemaErr)
	}
	// The seed, for shipping_method_versions: an order needs a shipping version
	// and the migration creates none.
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		panic(err)
	}
	if _, seedErr := pool.Exec(ctx, string(seed)); seedErr != nil {
		panic(seedErr)
	}
	code := m.Run()
	pool.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

// parcel is what left the warehouse and whether it ARRIVED — the two states
// this feature is bounded by.
type parcel struct {
	units   int
	arrived bool
}

// fixture is one customer's order: `ordered` units bought, one parcel carrying
// `p.units` of them, against a product covered for `months` (0 for a product
// with no term set).
type fixture struct {
	userID string
	number string
	lineID uuid.UUID
}

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
	var variantID uuid.UUID
	var term any
	if months > 0 {
		term = months
	}
	if err := tx.QueryRow(ctx, `
		WITH b AS (
			INSERT INTO brands (slug, name) VALUES ('wb-' || gen_random_uuid(), '保固品牌')
			RETURNING id
		), c AS (
			INSERT INTO categories (slug, name) VALUES ('wc-' || gen_random_uuid(), '保固分類')
			RETURNING id
		), p AS (
			INSERT INTO products (brand_id, category_id, slug, name, warranty_months)
			SELECT b.id, c.id, 'w-' || gen_random_uuid(), '保固測試商品', $1::integer
			FROM b, c
			RETURNING id
		)
		INSERT INTO product_variants (product_id, sku, price_cents)
		-- product_variants_sku_format allows only upper-case alphanumerics.
		SELECT p.id, 'W-' || upper(replace(gen_random_uuid()::text, '-', '')), 100000 FROM p
		RETURNING id`, term).Scan(&variantID); err != nil {
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
		                         unit_price_cents, quantity)
		VALUES ($1, $2, 'W-SKU', '保固測試商品', 100000, $3) RETURNING id`,
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
		// Dispatched ten days ago, delivered eight. The dates differ so a fixture
		// cannot go green whichever column the expiry is computed from.
		var delivered any
		if p.arrived {
			delivered = "8 days"
		}
		var shipmentID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
			VALUES ($1, '黑貓', 'TW-'||$2, now() - interval '10 days',
			        CASE WHEN $3::text IS NULL THEN NULL ELSE now() - $3::interval END)
			RETURNING id`,
			orderID, number, delivered).Scan(&shipmentID); err != nil {
			t.Fatalf("create shipment: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, p.units); err != nil {
			t.Fatalf("create shipment line: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return fixture{userID: userID.String(), number: number, lineID: lineID}
}

// TestRegistrationIsBoundedByWhatArrived proves cover cannot start before the
// goods reach somebody: a clock started in transit loses the customer those days.
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

// TestTheFormOffersNothingWhileTheParcelIsInTransit is the READ side of the same
// boundary: the page has to say so before somebody presses a button.
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

	// The control.
	withTerm := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(t.Context(), withTerm.lineID.String(), withTerm.userID, "", 1); err != nil {
		t.Errorf("a product with a term was refused: %v", err)
	}
}

// TestTheExpiryRunsFromDeliveryAndNotFromDispatch compares DATES, not a month
// count: two days do not add a month. The negative half is what makes it a lock,
// since a fixture stamping both timestamps at once would otherwise go green.
func TestTheExpiryRunsFromDeliveryAndNotFromDispatch(t *testing.T) {
	ctx := t.Context()
	s := warranty.NewStore(pool)
	f := newFixture(t, 1, 24, parcel{units: 1, arrived: true})

	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 1); err != nil {
		t.Fatalf("register: %v", err)
	}

	var fromDelivery, fromDispatch bool
	if err := pool.QueryRow(ctx, `
		SELECT w.expires_on = (s.delivered_at::date + interval '24 months')::date,
		       w.expires_on = (s.shipped_at::date   + interval '24 months')::date
		FROM warranty_registrations w
		JOIN order_shipment_lines sl ON sl.order_line_id = w.order_line_id
		JOIN order_shipments s ON s.id = sl.shipment_id
		WHERE w.order_line_id = $1`, f.lineID).Scan(&fromDelivery, &fromDispatch); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !fromDelivery {
		t.Error("the cover does not run 24 months from the day the parcel arrived")
	}
	if fromDispatch {
		t.Error("the cover runs from the DISPATCH date, which is short by the time " +
			"in transit — and the days lost come off the customer")
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
