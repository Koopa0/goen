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
	// and the migration creates none. The catalogue it also loads is unused
	// here — every case builds its own product, so a case that changes a
	// warranty term does not change it for the others.
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

// parcel is what left the warehouse and whether it ARRIVED.
//
// Two fields rather than one count, because the distinction is the whole of
// what this feature is bounded by and the fixture could not express it before:
// every parcel it built was dispatched, so "shipped but not yet delivered" —
// the state a customer is in for one to three days on every order — was a state
// no test could reach.
type parcel struct {
	units   int
	arrived bool
}

// fixture is one customer's order: `ordered` units bought, one parcel carrying
// `p.units` of them, against a product covered for `months` (0 for a product
// with no term set).
//
// The gap between ordered and shipped is one point: a warranty is bounded by
// what reached somebody, so an order where those numbers are equal cannot tell
// the two ceilings apart. The gap between shipped and DELIVERED is the other,
// and it is the one that moves the expiry date.
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
	// The brand and category are created here rather than assumed: TestMain
	// loads the migration and not the seed, so nothing exists to reference.
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
		-- product_variants_sku_format allows only upper-case alphanumerics and
		-- hyphens, so the uuid is upper-cased rather than pasted.
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

	// order_has_delivery_details is DEFERRED and fires at commit: an order
	// nobody can deliver to is not an order, and the guard says so at the one
	// moment the whole picture exists.
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'w@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create delivery details: %v", err)
	}

	if p.units > 0 {
		// Dispatched ten days ago and, when it arrived, delivered EIGHT days ago.
		// The two dates differ on purpose: an expiry computed from the wrong one
		// is two months plus two days rather than two months plus ten, and a
		// fixture that stamped both at the same instant could not tell them
		// apart — which is the whole defect this fixture now has to be able to
		// see.
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
// goods reach somebody.
//
// A warranty starts on DELIVERY. Registering cover for a box in transit starts
// the clock early, and the days it loses come off the customer's cover, not the
// shop's — the same direction every reading in this feature errs in.
//
// The "in transit" row is the one this could not ask before: order_shipments
// carried delivered_at from the day the schema was written and NOTHING wrote it,
// so every parcel in the world looked identical to this query and the boundary
// had no second side.
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
// boundary.
//
// Register refusing is not enough: the page has to say so before somebody
// presses a button. A customer whose parcel is two days out reads a line
// explaining that registration opens on delivery, rather than a form that
// refuses them.
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

	// The control, and it is the half that makes the case above mean anything: an
	// identical order whose parcel ARRIVED is offered. Without it, a query that
	// returned nothing for every order would pass the assertion above.
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
//
// warranty_months is NULL when the shop has not stated a term, and that is a
// real state rather than a missing one. Defaulting it would have goen inventing
// a promise the shop never made — and expires_on is NOT NULL, so something has
// to give: this is what gives.
func TestAProductWithNoTermCannotBeRegistered(t *testing.T) {
	s := warranty.NewStore(pool)

	noTerm := newFixture(t, 1, 0, parcel{units: 1, arrived: true})
	err := s.Register(t.Context(), noTerm.lineID.String(), noTerm.userID, "", 1)
	// ErrNotRegistrable specifically, not merely "an error". expires_on is NOT
	// NULL, so a missing term ALSO produces a not-null violation further down —
	// and a case that only asked whether something failed stayed green with the
	// explicit guard deleted. The difference reaches the customer: one is "this
	// cannot be registered", the other is a 500.
	if !errors.Is(err, warranty.ErrNotRegistrable) {
		t.Errorf("a product with no stated term gave %v, want ErrNotRegistrable — "+
			"either goen invented a warranty, or it refused one by crashing", err)
	}

	// The control: the same shape WITH a term registers.
	withTerm := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	if err := s.Register(t.Context(), withTerm.lineID.String(), withTerm.userID, "", 1); err != nil {
		t.Errorf("a product with a term was refused: %v", err)
	}
}

// TestTheExpiryRunsFromDeliveryAndNotFromDispatch is the lock on the clock.
//
// Two things at once, and the second is the one that had been wrong: the expiry
// is never a value a customer supplies — a form that could carry one is a form
// that could choose one — and it is measured from the day the parcel ARRIVED.
//
// It compares DATES rather than a month count, and that is not fussiness. The
// version this replaces asserted
// `EXTRACT(YEAR FROM age(expires_on, shipped_at)) * 12 + EXTRACT(MONTH ...) == 24`,
// which is 24 whether the term is counted from dispatch or from delivery: the
// two days between them do not add a month, so the assertion was blind to
// exactly the defect it sat next to. An expiry two days short is two days of
// cover taken off a customer, and no test in this file could see it.
//
// The second assertion is what makes the first one a lock. Without
// `fromDispatch == false`, a fixture that ever stamped both timestamps at one
// instant would go green while proving nothing about which column was read.
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

// TestOnlyTheOwnerCanRegisterOrSee proves the scope is in the query.
//
// Ownership is a WHERE clause, not a check in Go. A form that read the order
// and then asked who owned it is a check somebody skips by posting straight to
// the endpoint — which is exactly what this does.
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
	// And an order that does not exist gives the SAME answer, so the two cannot
	// be told apart by somebody probing order numbers.
	if _, err := s.Registrable(ctx, "GOEN-NOPE-0001", mine.userID); !errors.Is(err, warranty.ErrNotFound) {
		t.Errorf("an absent order read as %v, want ErrNotFound", err)
	}

	// The owner can.
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
	// The OTHER unit still can be.
	if err := s.Register(ctx, f.lineID.String(), f.userID, "", 2); err != nil {
		t.Errorf("the second unit was refused: %v", err)
	}
}

// TestASerialNumberIsRegisteredOnceAcrossTheWholeShop proves a mistyped serial
// is caught, and an absent one is not a duplicate.
//
// A serial identifies one physical device. Two registrations claiming one
// serial means somebody mistyped — and the message says so, because the other
// reading (two devices genuinely share a serial) is not something a customer
// can act on.
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

	// An EMPTY serial is not a duplicate of another empty one: the unique index
	// is partial, and several customers legitimately register without one.
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
