//go:build integration

package db_test

import (
	"slices"
	"testing"
)

// definerFunctions is every function in public that runs with its owner's
// privileges. Changing this list is a reviewed security decision: a definer is
// how the app does what its own role has no grant to do, so adding one widens
// what the app can reach and removing one breaks a caller that relied on it.
// A function that silently loses SECURITY DEFINER in a later CREATE OR REPLACE
// turns this red.
//
// The names are written out, not read from the migration text: a list computed
// from the same source it checks would agree with whatever that source said.
var definerFunctions = []string{
	"alarm_invoice_operation(uuid,uuid,text)",
	"attribute_complete_payment_paid(text)",
	"authorize_invoice_allowance_resend(uuid,uuid,text)",
	"award_loyalty_points(uuid)",
	"cancel_payment(text)",
	"canonical_invoice_lines(uuid)",
	"capture_payment(text,bigint,text,text)",
	"claim_invoice_allowance(uuid,uuid,uuid,text)",
	"claim_invoice_issue(text,uuid,text)",
	"claim_invoice_void(uuid,text,uuid,text)",
	"claim_return_refund_execution(uuid,uuid,text)",
	"compensate_return_with_credit(uuid,bigint,uuid)",
	"consume_reservation(uuid)",
	"consume_reservation_partial(uuid,integer)",
	"erase_user(uuid)",
	"grant_store_credit(uuid,bigint,text,uuid,uuid)",
	"hold_inventory(uuid,uuid,integer,interval,text)",
	"lease_invoice_operation(uuid,uuid,interval)",
	"lock_cart_catalogue(uuid)",
	"lock_coupon_for_checkout(text)",
	"lock_stock_notice_variant(uuid,text)",
	"lock_store_credit_for_checkout(uuid)",
	"mark_invoice_operation_sent(uuid,uuid)",
	"mark_payment_event_unreconciled(text,text)",
	"open_payment(uuid,text,bigint)",
	"open_refund_before_shipment(text,text,uuid,text)",
	"open_unsent_refund(text,uuid[],integer[],text,text,uuid,text)",
	"post_store_credit(uuid,bigint,text,uuid,text,uuid)",
	"product_variants_queue_restock()",
	"reconcile_invalid_invoice_allowance(uuid,uuid,uuid,text,text,timestamp with time zone,bigint,text[],integer[],bigint[],bigint[])",
	"record_audit_event(uuid,text,text,uuid,jsonb,jsonb,text)",
	"record_complete_payment(uuid,text,bigint)",
	"record_expired_payment(uuid,text,bigint)",
	"record_invalid_invoice_allowance(uuid,uuid,text,text,timestamp with time zone,bigint,text[],integer[],bigint[],bigint[])",
	"record_inventory_movement(uuid,integer,text,text,text,uuid,uuid)",
	"record_invoice_operation_audit(uuid,uuid,jsonb)",
	"record_refund_api_rejection(uuid,uuid,text)",
	"record_refund_cancelled(uuid,text,uuid,text)",
	"record_refund_failed(uuid,text,uuid,text)",
	"record_refund_pending(uuid,text,uuid,text)",
	"record_refund_requires_action(uuid,text,uuid,text)",
	"record_refund_succeeded(uuid,text,uuid,text)",
	"redeem_coupon(uuid,uuid,uuid,bigint)",
	"redeem_loyalty_points(uuid,bigint,uuid)",
	"refresh_copurchases()",
	"reject_invoice_operation(uuid,uuid,text)",
	"release_complete_payment(text)",
	"release_payment_event(text)",
	"release_reservation(uuid)",
	"reschedule_invoice_operation(uuid,uuid,text,interval)",
	"return_credit_owner_guard()",
	"return_lines_require_open_request()",
	"return_requests_recount()",
	"reverse_order_credit(uuid)",
	"reverse_order_points(uuid)",
	"reverse_return_points(uuid)",
	"revoke_staff(uuid)",
	"secure_promoted_account(uuid)",
	"settle_invoice_allowance(uuid,uuid,text,timestamp with time zone,text[],integer[],bigint[],bigint[],jsonb)",
	"settle_invoice_issue(uuid,uuid,text,text,timestamp with time zone,text[],integer[],bigint[],bigint[])",
	"settle_invoice_void(uuid,uuid)",
	"settle_unsent_refund(uuid,uuid,text)",
	"spend_store_credit(uuid,bigint)",
	"sweep_notified_stock_notifications(interval,integer)",
	"upsert_staff(text,text,text)",
}

func TestSecurityDefinerFunctionsAreTheReviewedSet(t *testing.T) {
	t.Parallel()

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.oid::regprocedure::text
		FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace AND p.prosecdef`)
	if err != nil {
		t.Fatalf("list security definer functions: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var sig string
		if scanErr := rows.Scan(&sig); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		got = append(got, sig)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("read security definer functions: %v", err)
	}
	slices.Sort(got)

	want := slices.Clone(definerFunctions)
	slices.Sort(want)
	if len(want) != 65 {
		t.Fatalf("golden list holds %d signatures, want 65; edit the list on purpose, not by accident", len(want))
	}

	for _, sig := range want {
		if !slices.Contains(got, sig) {
			t.Errorf("%s is no longer SECURITY DEFINER", sig)
		}
	}
	for _, sig := range got {
		if !slices.Contains(want, sig) {
			t.Errorf("%s is SECURITY DEFINER but is not in the reviewed list", sig)
		}
	}
}
