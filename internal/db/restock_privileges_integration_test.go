//go:build integration

package db_test

import "testing"

// The restock retention sweep is a SECURITY DEFINER that deletes from a table
// no login may delete from, so who can call it is the whole control: the
// background role only.
func TestOnlyMaintenanceMayRunTheRestockRetentionSweep(t *testing.T) {
	const sweep = "sweep_notified_stock_notifications(interval,integer)"
	assertFunctionPrivilege(t, "maintenance", sweep, true)
	for _, role := range []string{"store", "admin", "reporting"} {
		assertFunctionPrivilege(t, role, sweep, false)
	}
	for _, role := range []string{"store", "admin", "maintenance", "reporting"} {
		var got bool
		if err := schemaPool(t).QueryRow(t.Context(),
			`SELECT has_table_privilege($1, 'stock_notifications', 'DELETE')`, role).Scan(&got); err != nil {
			t.Fatalf("read %s DELETE on stock_notifications: %v", role, err)
		}
		if got {
			t.Errorf("%s can DELETE stock_notifications directly; the sweep is the only door", role)
		}
	}
}
