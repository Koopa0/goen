//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRefundExecutionFunctionsExposeOnlyNarrowAdminDoors(t *testing.T) {
	narrowDoors := []string{
		"claim_return_refund_execution(uuid,uuid,text)",
		"record_refund_pending(uuid,text,uuid,text)",
		"record_refund_requires_action(uuid,text,uuid,text)",
		"record_refund_succeeded(uuid,text,uuid,text)",
		"record_refund_failed(uuid,text,uuid,text)",
		"record_refund_cancelled(uuid,text,uuid,text)",
		"record_refund_api_rejection(uuid,uuid,text)",
	}
	privateEngine := "apply_refund_provider_outcome(uuid,text,text,uuid,text,boolean)"

	for _, role := range []string{"store", "reporting", "maintenance"} {
		for _, signature := range append(narrowDoors, privateEngine) {
			assertFunctionPrivilege(t, role, signature, false)
		}
	}
	for _, signature := range narrowDoors {
		assertFunctionPrivilege(t, "admin", signature, true)
	}
	assertFunctionPrivilege(t, "admin", privateEngine, false)
}

func assertFunctionPrivilege(t *testing.T, role, signature string, want bool) {
	t.Helper()
	var got bool
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, signature).
		Scan(&got); err != nil {
		t.Fatalf("read %s privilege on %s: %v", role, signature, err)
	}
	if got != want {
		t.Errorf("%s execute on %s = %v, want %v", role, signature, got, want)
	}
}

// TestOnlyTheDoorOpensARefundBeforeShipment holds the one grant the refund
// before shipment added: admin executes the door, and no role can mark a
// return as one by writing the column itself.
func TestOnlyTheDoorOpensARefundBeforeShipment(t *testing.T) {
	const door = "open_refund_before_shipment(text,text,uuid,text)"
	assertFunctionPrivilege(t, "admin", door, true)
	for _, role := range []string{"store", "reporting", "maintenance"} {
		assertFunctionPrivilege(t, role, door, false)
	}
	for _, role := range []string{"store", "admin", "reporting", "maintenance"} {
		for _, privilege := range []string{"INSERT", "UPDATE"} {
			var got bool
			if err := schemaPool(t).QueryRow(t.Context(),
				`SELECT has_column_privilege($1, 'return_requests', 'before_shipment', $2)`,
				role, privilege).Scan(&got); err != nil {
				t.Fatalf("read %s %s on before_shipment: %v", role, privilege, err)
			}
			if got {
				t.Errorf("%s can %s return_requests.before_shipment", role, privilege)
			}
		}
	}
}

// TestOnlyTheDoorsOpenAndSettleAnUnsentRefund holds the two grants the unsent
// refund added: admin executes both doors, no other role executes either, and
// no role can mark a return as unsent by writing the column itself.
func TestOnlyTheDoorsOpenAndSettleAnUnsentRefund(t *testing.T) {
	for _, door := range []string{
		"open_unsent_refund(text,uuid[],integer[],text,text,uuid,text)",
		"settle_unsent_refund(uuid,uuid,text)",
	} {
		assertFunctionPrivilege(t, "admin", door, true)
		for _, role := range []string{"store", "reporting", "maintenance"} {
			assertFunctionPrivilege(t, role, door, false)
		}
	}
	for _, role := range []string{"store", "admin", "reporting", "maintenance"} {
		for _, privilege := range []string{"INSERT", "UPDATE"} {
			var got bool
			if err := schemaPool(t).QueryRow(t.Context(),
				`SELECT has_column_privilege($1, 'return_requests', 'unsent', $2)`,
				role, privilege).Scan(&got); err != nil {
				t.Fatalf("read %s %s on unsent: %v", role, privilege, err)
			}
			if got {
				t.Errorf("%s can %s return_requests.unsent", role, privilege)
			}
		}
	}
}

// TestADecidedReturnKeepsItsOrderAndReason: settle_unsent_refund reads an
// unsent refund's reason to write its units off or put them back on sale.
// The storefront files a customer's request as before and can change no
// request afterwards; a door, which runs as the owner, cannot change a
// decided request's order or reason either.
func TestADecidedReturnKeepsItsOrderAndReason(t *testing.T) {
	ctx := t.Context()
	o := newUnsentOrder(t, unsentOrderTotal)
	unsentID := o.mustOpen(t, "lost", unsentClaim{o.lineB, 2})

	// The customer returns the unit of b that arrived, as the storefront does.
	var customerID uuid.UUID
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatalf("set role store: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO return_requests (order_id, requested_by_user_id, reason)
	                            VALUES ($1, $2, '不需要了') RETURNING id`, o.id, o.user).Scan(&customerID); err != nil {
		t.Fatalf("the customer files a return: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
	                           VALUES ($1, $2, $3, 1)`, o.id, customerID, o.lineB); err != nil {
		t.Fatalf("the customer files a return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the customer's return: %v", err)
	}
	if err := asAdminRole(t, nil, `UPDATE return_requests SET status = 'approved', decided_at = now()
	                               WHERE id = $1`, customerID); err != nil {
		t.Fatalf("approve the customer's return: %v", err)
	}

	if roleHoldsTableVerb(t, "store", "return_requests", "UPDATE") {
		t.Error("store holds UPDATE on return_requests; no storefront statement updates a return")
	}

	// rewrite runs stmt on the request id as role, or as the owner when role is
	// empty, after filing the request first when file is set.
	rewrite := func(role, file, stmt string, id uuid.UUID) error {
		t.Helper()
		tx, err := schemaPool(t).Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if file != "" {
			if scanErr := tx.QueryRow(ctx, file, o.id).Scan(&id); scanErr != nil {
				t.Fatalf("file a request: %v", scanErr)
			}
		}
		if role != "" {
			if _, roleErr := tx.Exec(ctx, "SET LOCAL ROLE "+role); roleErr != nil {
				t.Fatalf("set role %s: %v", role, roleErr)
			}
		}
		_, err = tx.Exec(ctx, stmt, id)
		return err
	}
	for _, request := range []struct {
		name string
		id   uuid.UUID
	}{{"the unsent refund", unsentID}, {"the customer's return", customerID}} {
		for _, stmt := range []string{
			`UPDATE return_requests SET reason = 'customer_withdrew' WHERE id = $1`,
			`UPDATE return_requests SET order_id = '` + uuid.NewString() + `' WHERE id = $1`,
		} {
			err := rewrite("store", "", stmt, request.id)
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
				t.Errorf("store, %s: %s failed with %v, want PgError 42501", request.name, stmt, err)
			}
			requireRule(t, "the owner, "+request.name+": "+stmt, rewrite("", "", stmt, request.id),
				"return_requests_claim_frozen")
		}
	}

	// The door's own window: an unsent request is written as requested and
	// approved in one transaction, and its code is fixed from the start.
	requireRule(t, "the reason of an unsent refund before approval", rewrite("",
		`INSERT INTO return_requests (order_id, reason, unsent) VALUES ($1, 'lost', true) RETURNING id`,
		`UPDATE return_requests SET reason = 'customer_withdrew' WHERE id = $1`, uuid.Nil),
		"return_requests_claim_frozen")
	// Nor does the statement that decides a request change what it decides on.
	requireRule(t, "the reason in the deciding statement", rewrite("",
		`INSERT INTO return_requests (order_id, reason) VALUES ($1, '不需要了') RETURNING id`,
		`UPDATE return_requests SET status = 'rejected', decided_at = now(), reason = '改變心意'
		 WHERE id = $1`, uuid.Nil),
		"return_requests_claim_frozen")
}
