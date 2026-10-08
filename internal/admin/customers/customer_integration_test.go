//go:build integration

package customers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/warranty"
)

func TestTheBackOfficeCanSeeOneCustomerWhole(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 50000)

	view, err := s.Profile(ctx, userID.String())
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if view.CreditCents != 50000 {
		t.Errorf("credit balance is %d, want 50000", view.CreditCents)
	}
	if view.Verified {
		t.Error("a customer nobody has verified reads as verified")
	}
	if view.Since == "" {
		t.Error("the page does not say when they registered")
	}
}
func TestLookingAtACustomerIsRecorded(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 1000)

	if _, err := s.Profile(ctx, userID.String()); err != nil {
		t.Fatalf("Customer: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'customer.view' AND entity_id = $1 AND actor_user_id = $2`,
		userID, staff).Scan(&rows); err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d audit rows for one lookup, want 1", rows)
	}

	var after string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce("after"::text, '') FROM audit_events
		WHERE action = 'customer.view' AND entity_id = $1`, userID).Scan(&after); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	if strings.Contains(after, addr) {
		t.Errorf("the trail copied the customer's address into itself: %s", after)
	}
}
func TestACustomerSearchNeedsATerm(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)

	for _, term := range []string{"", " ", "王"} {
		view, err := s.Search(ctx, term)
		if err != nil {
			t.Fatalf("Customers(%q): %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q reads as a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d customers without searching", term, len(view.Rows))
		}
	}
}
func TestACustomerIsFoundByTheStartOfTheirAddress(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 0)

	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}

	for _, term := range []string{
		string([]rune(addr)[:10]),
		strings.ToUpper(string([]rune(addr)[:10])),
	} {
		view, err := s.Search(ctx, term)
		if err != nil {
			t.Fatalf("Customers(%q): %v", term, err)
		}
		found := false
		for i := range view.Rows {
			if view.Rows[i].ID == userID.String() {
				found = true
			}
		}
		if !found {
			t.Errorf("searching %q did not find the customer", term)
		}
	}
}

// TestACustomerSearchTakesWildcardsLiterally holds the stance that the customer
// list is searched, never browsed: "%%" passes the two-rune floor and must not
// list every account, and a typed _ matches only an underscore.
func TestACustomerSearchTakesWildcardsLiterally(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	stem := strings.ReplaceAll(uuid.NewString(), "-", "")
	ids := map[string]uuid.UUID{}
	for _, addr := range []string{"lk_" + stem + "@goen.invalid", "lkx" + stem + "@goen.invalid"} {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role, full_name) VALUES ($1, 'customer', 'wildcard')
			RETURNING id`, addr).Scan(&id); err != nil {
			t.Fatalf("create %s: %v", addr, err)
		}
		ids[addr] = id
	}

	view, err := s.Search(ctx, "%%")
	if err != nil {
		t.Fatalf("Customers(%%%%): %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf(`searching "%%%%" listed %d customers; no address or name starts with it`, len(view.Rows))
	}

	view, err = s.Search(ctx, "lk_"+stem)
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	found := map[string]bool{}
	for i := range view.Rows {
		found[view.Rows[i].ID] = true
	}
	if !found[ids["lk_"+stem+"@goen.invalid"].String()] {
		t.Error("searching the underscored address did not find its customer")
	}
	if found[ids["lkx"+stem+"@goen.invalid"].String()] {
		t.Error("a typed _ matched a customer whose address has an x there")
	}
}
func TestACustomersSpendCountsOnlyCommittedOrders(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 0)

	paid := admintest.OrderForCustomer(t, pool, userID, 120000, true)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, 20000, '退貨', $2, $3, NULL)`,
		userID, paid, "customer-refund:"+paid.String()); err != nil {
		t.Fatalf("return part of the committed order: %v", err)
	}
	cancelled := admintest.OrderForCustomer(t, pool, userID, 990000, false)
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`,
		cancelled); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	view, err := s.Profile(ctx, userID.String())
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if view.SpentCents != 100000 {
		t.Errorf("spend is %d, want 100000 — the cancelled order or its refund is being counted",
			view.SpentCents)
	}
	if view.Orders != 2 {
		t.Errorf("order count is %d, want 2", view.Orders)
	}
}
func TestAPromotedCustomerIsStillFindable(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 0)
	admintest.OrderForCustomer(t, pool, userID, 50000, true)

	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`,
		userID); err != nil {
		t.Fatalf("promote: %v", err)
	}

	var addr string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).
		Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	view, err := s.Search(ctx, string([]rune(addr)[:10]))
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	found := false
	for i := range view.Rows {
		if view.Rows[i].ID == userID.String() {
			found = true
		}
	}
	if !found {
		t.Error("a promoted customer cannot be found by the search")
	}

	one, err := s.Profile(ctx, userID.String())
	if err != nil {
		t.Fatalf("Customer of a promoted customer: %v", err)
	}
	if one.SpentCents != 50000 {
		t.Errorf("spend is %d, want 50000", one.SpentCents)
	}
}

func TestTheShopCanFindAWarrantyTheCustomerRegistered(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	serial, number := registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []struct {
		name string
		q    string
	}{
		{name: "by the serial off the label", q: serial},
		{name: "by the order number off the confirmation mail", q: number},
	} {
		t.Run(term.name, func(t *testing.T) {
			view, err := customers.NewStore(pool).Warranties(ctx, term.q)
			if err != nil {
				t.Fatalf("search warranties: %v", err)
			}
			if !view.Searching() {
				t.Fatal("the page did not run a search for a term long enough to be one")
			}
			if len(view.Rows) != 1 {
				t.Fatalf("searching %q found %d registrations, want 1", term.q, len(view.Rows))
			}
			got := view.Rows[0]
			if got.Serial != serial || got.Order != number {
				t.Errorf("found serial %q on order %q, want %q on %q",
					got.Serial, got.Order, serial, number)
			}
			if !got.InForce {
				t.Error("a warranty registered today reads as expired")
			}
		})
	}

	view, err := customers.NewStore(pool).Warranties(ctx, "SN-NOSUCHTHING")
	if err != nil {
		t.Fatalf("search warranties: %v", err)
	}
	if len(view.Rows) != 0 {
		t.Errorf("an unregistered serial found %d rows, want 0", len(view.Rows))
	}
}

func TestTheWarrantyLookupRefusesToListEverything(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	registeredWarranty(t, "SN-"+strings.ToUpper(uuid.NewString()[:8]))

	for _, term := range []string{"", " ", "A"} {
		view, err := customers.NewStore(pool).Warranties(ctx, term)
		if err != nil {
			t.Fatalf("search warranties %q: %v", term, err)
		}
		if view.Searching() {
			t.Errorf("%q ran a search", term)
		}
		if len(view.Rows) != 0 {
			t.Errorf("%q listed %d registrations without being asked", term, len(view.Rows))
		}
	}
}

func registeredWarranty(t *testing.T, serial string) (registered, orderNumber string) {
	t.Helper()
	return registeredWarrantyQuantity(t, serial, 1)
}

func registeredWarrantyQuantity(t *testing.T, serial string, quantity int32) (registered, orderNumber string) {
	t.Helper()
	ctx := t.Context()

	var variantID, productID uuid.UUID
	var warrantyNote string
	var warrantyMonths int32
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, p.id, coalesce(p.warranty_note, ''), p.warranty_months
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND p.warranty_months IS NOT NULL
		ORDER BY pv.id LIMIT 1`).Scan(&variantID, &productID, &warrantyNote, &warrantyMonths); err != nil {
		t.Fatalf("find a variant of a product with a stated term: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('wr-' || gen_random_uuid() || '@goen.invalid', 'customer', '保固客戶')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (
			order_id, product_id, variant_id, sku, product_name,
			warranty_note, warranty_months, unit_price_cents, quantity
		)
		SELECT $1, $2, pv.id, pv.sku, p.name, nullif($4, ''), $5, 100000, $6
		FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $3 RETURNING order_lines.id`,
		orderID, productID, variantID, warrantyNote, warrantyMonths, quantity).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'wr@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_warranty_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, ref, int64(quantity)*100000); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, ref, int64(quantity)*100000); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	admintest.MoveOrderToShipped(t, tx, orderID)
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
		VALUES ($1, 'black_cat', 'WR-' || $2, now() - interval '5 days', now() - interval '3 days')
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, quantity); err != nil {
		t.Fatalf("create shipment line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := warranty.NewStore(pool).Register(
		ctx, lineID.String(), userID.String(), serial, 1); err != nil {
		t.Fatalf("register warranty: %v", err)
	}
	return serial, number
}

// The page and the customer's own account judge tiers by member_spend over the
// membership window, so the two must show one figure: an order older than the
// window counts in the lifetime total and not here.
func TestTheCustomerPageShowsTheSpendTiersAreJudgedBy(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	ctx = i18n.WithLocale(ctx, i18n.ZhHant)
	s := customers.NewStore(pool)
	userID := admintest.CreditedAccount(t, pool, 0)
	admintest.OrderForCustomer(t, pool, userID, 120000, true)
	old := admintest.OrderForCustomer(t, pool, userID, 70000, true)
	if _, err := pool.Exec(ctx, `UPDATE orders SET placed_at = now() - make_interval(days => $2 + 1) WHERE id = $1`,
		old, loyalty.MembershipWindowDays); err != nil {
		t.Fatalf("age the order: %v", err)
	}

	var want int64
	if err := pool.QueryRow(ctx, `SELECT member_spend($1, $2, NULL)`, userID, loyalty.MembershipWindowDays).Scan(&want); err != nil {
		t.Fatalf("read member_spend: %v", err)
	}
	view, err := s.Profile(ctx, userID.String())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if want == 0 || view.WindowSpendCents != want {
		t.Errorf("window spend is %d, want member_spend %d (non-zero)", view.WindowSpendCents, want)
	}
	if view.WindowSpendCents >= view.SpentCents {
		t.Errorf("window spend %d is not below lifetime spend %d, so the old order was counted", view.WindowSpendCents, view.SpentCents)
	}
	if view.NextTierName != "銀卡會員" || view.NextTierCents != 1000000 {
		t.Errorf("next tier is %q at %d, want the lowest band above the spend, 銀卡會員 at 1000000", view.NextTierName, view.NextTierCents)
	}
}
