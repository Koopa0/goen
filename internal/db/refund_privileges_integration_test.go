//go:build integration

package db_test

import "testing"

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
