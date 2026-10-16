//go:build integration

package db_test

import "testing"

// TestOrderTotalsWidensNoRolesRead: order_totals is security_invoker, so its
// grant lets a role read nothing it could not already read from orders and
// order_lines. The roles that state totals read it, store also through
// order_amount_after_credit as every /pay page does. A role that cannot read a
// column beneath the view cannot read the view.
func TestOrderTotalsWidensNoRolesRead(t *testing.T) {
	for _, role := range []string{"store", "admin", "reporting"} {
		expectAccepted(t, role+" reads order_totals", `SET LOCAL ROLE `+role+`;
			SELECT total_cents FROM order_totals WHERE order_id = `+shippedOrder)
	}
	expectAccepted(t, "store reads what an order owes", `SET LOCAL ROLE store;
		SELECT order_amount_after_credit(`+shippedOrder+`)`)
	expectRefusal(t, "reporting without orders.discount_cents", `
		REVOKE SELECT (discount_cents) ON orders FROM reporting;
		SET LOCAL ROLE reporting;
		SELECT total_cents FROM order_totals WHERE order_id = `+shippedOrder, "42501", "")
}
