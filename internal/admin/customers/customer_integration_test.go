//go:build integration

package customers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/customers"
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
