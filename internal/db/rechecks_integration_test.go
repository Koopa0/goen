//go:build integration

package db_test

import (
	"testing"
)

// These rules hold a fact other rules read as fixed. Each case runs inside the fixtures'
// rolled-back transaction, as the role whose statement it is: `SET LOCAL ROLE` in the
// statement, or the owner, which every door runs as, when none is set.

// expectRefusal runs stmt over the fixtures and requires the named refusal. code is the
// SQLSTATE; rule the constraint name, empty for a privilege refusal.
func expectRefusal(t *testing.T, what, stmt, code, rule string) {
	t.Helper()
	err := run(t, stmt)
	if err == nil {
		t.Errorf("%s: accepted, want %s %s", what, code, rule)
		return
	}
	if gotCode, gotRule := constraintViolation(err); gotCode != code || gotRule != rule {
		t.Errorf("%s: refused with %s %q, want %s %q: %v", what, gotCode, gotRule, code, rule, err)
	}
}

func expectAccepted(t *testing.T, what, stmt string) {
	t.Helper()
	if err := run(t, stmt); err != nil {
		t.Errorf("%s: refused: %v", what, err)
	}
}

const (
	shippedOrder   = "'66666666-6666-4666-8666-666666666666'"
	pendingOrder   = "'6666aaaa-6666-4666-8666-666666666666'"
	pickingOrder   = "'6666bbbb-6666-4666-8666-666666666666'"
	fixtureParcel  = "'66660002-0000-4000-8000-000000000000'"
	fixtureProduct = "'33333333-3333-4333-8333-333333333333'"
	ming           = "'55555555-5555-4555-8555-555555555555'"
	hua            = "'5555aaaa-5555-4555-8555-555555555555'"
)

// TestOrdersStorefrontCancelsOnly: role store can move an order's
// fulfillment_status only from pending to cancelled: every other move,
// including the ones orders_legal_transition allows, is refused by
// orders_storefront_cancels_only.
//
// Every other move, including the ones orders_legal_transition allows, is
// refused to it, so it cannot report a parcel the back office never recorded.
func TestOrdersStorefrontCancelsOnly(t *testing.T) {
	const refused = "orders_storefront_cancels_only"
	for _, tc := range []struct{ name, stmt string }{
		{"pending to picking",
			`UPDATE orders SET fulfillment_status = 'picking' WHERE id = ` + pendingOrder},
		{"picking to shipped",
			`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = ` + pickingOrder},
		{"shipped to delivered",
			`UPDATE orders SET fulfillment_status = 'delivered' WHERE id = ` + shippedOrder},
		{"shipped to completed",
			`UPDATE orders SET fulfillment_status = 'completed' WHERE id = ` + shippedOrder},
		{"picking to cancelled",
			`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = ` + pickingOrder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectRefusal(t, "store", `SET LOCAL ROLE store; `+tc.stmt, "23514", refused)
			// A login that inherits store's grants without SET ROLE is the storefront too.
			expectRefusal(t, "a login in store", `CREATE ROLE recheck_store_login IN ROLE store;
				SET LOCAL ROLE recheck_store_login; `+tc.stmt, "23514", refused)
		})
	}
	expectAccepted(t, "store cancels a pending order", `SET LOCAL ROLE store;
		UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = `+pendingOrder)
	expectAccepted(t, "admin ships a picked order", `SET LOCAL ROLE admin;
		UPDATE orders SET fulfillment_status = 'shipped' WHERE id = `+pickingOrder)
	expectAccepted(t, "the owner ships a picked order",
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = `+pickingOrder)
}

// TestOrderShipmentsMomentsFrozen: a parcel's shipped_at and delivered_at,
// which the return window and warranty cover count from, are written once:
// order_shipments_moments_frozen refuses the owner and role admin a second
// write.
//
// Return_window_ends, warranty cover and the return
// form count from these two moments. Dispatch writes shipped_at and the delivered stamp
// writes delivered_at once, where it finds NULL.
func TestOrderShipmentsMomentsFrozen(t *testing.T) {
	const (
		frozen = "order_shipments_moments_frozen"
		stamp  = `UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
		          WHERE id = ` + fixtureParcel + ` AND delivered_at IS NULL;`
	)
	expectAccepted(t, "admin stamps the delivery", `SET LOCAL ROLE admin; `+stamp)
	for _, role := range []string{"admin", ""} {
		as := "the owner"
		set := ""
		if role != "" {
			as, set = role, `SET LOCAL ROLE `+role+`; `
		}
		expectRefusal(t, as+" clears the delivery", stamp+set+
			`UPDATE order_shipments SET delivered_at = NULL WHERE id = `+fixtureParcel, "23514", frozen)
		expectRefusal(t, as+" moves the delivery", stamp+set+
			`UPDATE order_shipments SET delivered_at = delivered_at + interval '1 day' WHERE id = `+fixtureParcel,
			"23514", frozen)
	}
	expectRefusal(t, "the owner moves the dispatch",
		`UPDATE order_shipments SET shipped_at = shipped_at - interval '1 day' WHERE id = `+fixtureParcel,
		"23514", frozen)
	expectRefusal(t, "admin moves the dispatch", `SET LOCAL ROLE admin;
		UPDATE order_shipments SET shipped_at = shipped_at - interval '1 day' WHERE id = `+fixtureParcel,
		"42501", "")
	expectAccepted(t, "a re-run stamp changes nothing", stamp+`SET LOCAL ROLE admin; `+stamp)
}

// TestNewsletterIssuesSentStaysSent: a sent newsletter issue keeps its sent_at,
// recipients and text: newsletter_issues_sent_stays_sent and
// newsletter_issues_frozen_once_sent refuse the owner and role admin, while the
// sender's erasure may still clear sent_by.
//
// The text freeze reads sent_at, so sent_at and the count it was
// sent to are frozen with it.
func TestNewsletterIssuesSentStaysSent(t *testing.T) {
	const (
		compose = `SET LOCAL ROLE admin;
		           INSERT INTO newsletter_issues (subject, body) VALUES ('十月新品', '本月新品上架。');`
		send = `UPDATE newsletter_issues SET sent_at = now(), recipients = 3, sent_by = NULL
		        WHERE sent_at IS NULL;`
	)
	expectAccepted(t, "admin composes and sends", compose+send)
	for _, tc := range []struct{ name, stmt, rule string }{
		{"unsends it", `UPDATE newsletter_issues SET sent_at = NULL, recipients = 0;`, "newsletter_issues_sent_stays_sent"},
		{"redates it", `UPDATE newsletter_issues SET sent_at = sent_at - interval '1 day';`, "newsletter_issues_sent_stays_sent"},
		{"recounts it", `UPDATE newsletter_issues SET recipients = 5;`, "newsletter_issues_sent_stays_sent"},
		{"rewrites it", `UPDATE newsletter_issues SET subject = '改寫';`, "newsletter_issues_frozen_once_sent"},
	} {
		// admin holds UPDATE on the send columns, which the send writes, and not on the text.
		if tc.rule == "newsletter_issues_frozen_once_sent" {
			expectRefusal(t, "admin "+tc.name, compose+send+tc.stmt, "42501", "")
		} else {
			expectRefusal(t, "admin "+tc.name, compose+send+tc.stmt, "23514", tc.rule)
		}
		expectRefusal(t, "the owner "+tc.name, compose+send+`RESET ROLE; `+tc.stmt, "23514", tc.rule)
	}
	// The sender's erasure clears sent_by through its foreign key, which the freeze leaves alone.
	expectAccepted(t, "the sender is cleared", compose+send+`RESET ROLE; UPDATE newsletter_issues SET sent_by = NULL;`)
}

// TestProductReviewsSubjectFrozen: a review keeps its product and author once
// is_verified_purchase is checked against them: product_reviews_subject_frozen
// refuses the owner and role store.
//
// Is_verified_purchase is checked against the author's
// committed order for that product when it is set, so neither may change afterwards. Erasure
// clears the author through the foreign key.
func TestProductReviewsSubjectFrozen(t *testing.T) {
	const (
		frozen = "product_reviews_subject_frozen"
		review = `INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
		          VALUES (` + fixtureProduct + `, ` + ming + `, 5, '真的買了', true);`
		other = `INSERT INTO products (id, brand_id, category_id, slug, name)
		         VALUES ('3333dddd-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111',
		                 '22222222-2222-4222-8222-222222222222', 'recheck-other-product', '另一件商品');`
	)
	expectRefusal(t, "the owner moves it to another product", review+other+
		`UPDATE product_reviews SET product_id = '3333dddd-3333-4333-8333-333333333333'`, "23514", frozen)
	expectRefusal(t, "the owner gives it another author", review+
		`UPDATE product_reviews SET user_id = `+hua, "23514", frozen)
	expectRefusal(t, "the owner gives it another author and drops the badge", review+
		`UPDATE product_reviews SET user_id = `+hua+`, is_verified_purchase = false`, "23514", frozen)
	expectRefusal(t, "store moves it", review+`SET LOCAL ROLE store;
		UPDATE product_reviews SET product_id = product_id, user_id = user_id`, "42501", "")
	expectAccepted(t, "admin hides it", review+`SET LOCAL ROLE admin; UPDATE product_reviews SET hidden_at = now()`)
	expectAccepted(t, "the author is erased", review+`SET LOCAL ROLE store; SELECT erase_user(`+ming+`)`)
}

// TestOrderMoneyFrozenAfterClaim: once claim_invoice_issue has claimed it, a
// store-credit-funded order, which carries no payment and stays pending, keeps
// its totals and locale: orders_money_frozen_once_committed refuses the owner
// and role store gets insufficient_privilege.
//
// An order store credit pays in full carries no
// payment and stays pending until it is picked, so it is neither committed nor settled. Its
// issue is claimed before then, from these columns, and from the claim they are the invoice's.
func TestOrderMoneyFrozenAfterClaim(t *testing.T) {
	const (
		funded = mingPendingOrder + `
			INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
			VALUES ('6666cccc-6666-4666-8666-666666666666', 'member_carrier', '王小明', 'ming@example.com');
			SELECT post_store_credit(` + ming + `, 3690000, '測試發放', NULL, 'grant:recheck', NULL);
			SELECT spend_store_credit('6666cccc-6666-4666-8666-666666666666', -3690000);`
		claim  = `SELECT claim_invoice_issue('GO-260721-000390', NULL, 'recheck-claim');`
		change = `UPDATE orders SET shipping_cents = shipping_cents + 999
		          WHERE id = '6666cccc-6666-4666-8666-666666666666';`
	)
	expectAccepted(t, "the funded order before its claim", funded+change)
	expectRefusal(t, "the funded order after its claim", funded+claim+change,
		"23514", "orders_money_frozen_once_committed")
	expectRefusal(t, "the locale after its claim", funded+claim+
		`UPDATE orders SET locale = 'en' WHERE id = '6666cccc-6666-4666-8666-666666666666';`,
		"23514", "orders_money_frozen_once_committed")
	expectRefusal(t, "store after its claim", funded+claim+`SET LOCAL ROLE store; `+change, "42501", "")
}
