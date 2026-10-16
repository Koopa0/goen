//go:build integration

package db_test

import "testing"

// TestOrderSpendWidensNoRolesRead: committed_order_spend and member_order_spend
// are security_invoker, so their grants let a role read nothing it could not
// already read beneath them. store and admin read both, store also through
// member_tier as the account page and the points award do. reporting has no
// grant; given one, and the window, it still cannot read either view without
// a column beneath.
func TestOrderSpendWidensNoRolesRead(t *testing.T) {
	for _, view := range []string{"committed_order_spend", "member_order_spend"} {
		for _, role := range []string{"store", "admin"} {
			expectAccepted(t, role+" reads "+view, `SET LOCAL ROLE `+role+`;
				SELECT spend_cents FROM `+view+` WHERE user_id = `+ming)
		}
		expectRefusal(t, "reporting reads "+view, `SET LOCAL ROLE reporting;
			SELECT spend_cents FROM `+view, "42501", "")
		expectRefusal(t, "reporting granted "+view+", without orders.placed_at", `
			GRANT SELECT ON committed_order_spend, member_order_spend TO reporting;
			GRANT EXECUTE ON FUNCTION membership_window_days() TO reporting;
			REVOKE SELECT (placed_at) ON orders FROM reporting;
			SET LOCAL ROLE reporting;
			SELECT spend_cents FROM `+view, "42501", "")
	}
	for _, role := range []string{"store", "admin"} {
		expectAccepted(t, role+" reads a customer's tier", `SET LOCAL ROLE `+role+`;
			SELECT member_tier(`+ming+`, NULL), membership_tier_for(0)`)
	}
}
