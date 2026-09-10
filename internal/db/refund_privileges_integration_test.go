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
