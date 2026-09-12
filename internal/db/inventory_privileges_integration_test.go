//go:build integration

package db_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	recordInventoryMovementSig   = "record_inventory_movement(uuid,integer,text,text,text,uuid,uuid)"
	consumeReservationSig        = "consume_reservation(uuid)"
	consumeReservationPartialSig = "consume_reservation_partial(uuid,integer)"
	redeemCouponSig              = "redeem_coupon(uuid,uuid,uuid,bigint)"
	holdInventorySig             = "hold_inventory(uuid,uuid,integer,interval,text)"
	releaseReservationSig        = "release_reservation(uuid)"
)

// unusedFunctionDoors is the production role × EXECUTE pairs with no caller in
// query.sql.
var unusedFunctionDoors = []struct {
	role, signature, call string
}{
	{"store", recordInventoryMovementSig,
		`SELECT record_inventory_movement(
			'44444444-4444-4444-8444-444444444444', 999, 'receipt', 'k-store-receipt', NULL, NULL, NULL)`},
	{"store", consumeReservationSig,
		`SELECT consume_reservation('00000000-0000-4000-8000-000000000001')`},
	{"admin", consumeReservationSig,
		`SELECT consume_reservation('00000000-0000-4000-8000-000000000001')`},
	{"admin", redeemCouponSig,
		`SELECT redeem_coupon(
			'cccc0009-0000-4000-8000-000000000009',
			'6666aaaa-6666-4666-8666-666666666666',
			NULL, 20000)`},
}

// keptFunctionDoors must stay executable: admin staff stock, admin dispatch,
// storefront checkout coupon, and the DEFINER hold/release path.
var keptFunctionDoors = []struct {
	role, signature string
}{
	{"admin", recordInventoryMovementSig},
	{"admin", consumeReservationPartialSig},
	{"store", redeemCouponSig},
	{"store", holdInventorySig},
	{"store", releaseReservationSig},
}

// TestUnusedFunctionDoorsStayRevoked holds the EXECUTE matrix derived from
// production SQL: storefront inventory goes through hold/release, fulfilment
// through consume_reservation_partial, checkout through redeem_coupon.
func TestUnusedFunctionDoorsStayRevoked(t *testing.T) {
	for _, door := range unusedFunctionDoors {
		assertFunctionPrivilege(t, door.role, door.signature, false)
	}
	for _, door := range keptFunctionDoors {
		assertFunctionPrivilege(t, door.role, door.signature, true)
	}

	for _, door := range unusedFunctionDoors {
		t.Run(door.role+"/"+door.signature, func(t *testing.T) {
			assertRoleStatementDenied(t, door.role, door.call)
		})
	}
}

// TestProductionQueriesOwnTheFunctionExecuteMatrix refuses an EXECUTE grant
// whose function no production query of that role calls, and refuses a missing
// grant for a function a production query does call. Owner and test-role
// statements are not callers.
func TestProductionQueriesOwnTheFunctionExecuteMatrix(t *testing.T) {
	scoped := []string{
		"record_inventory_movement",
		"consume_reservation",
		"consume_reservation_partial",
		"redeem_coupon",
		"hold_inventory",
		"release_reservation",
	}
	signatures := map[string]string{
		"record_inventory_movement":   recordInventoryMovementSig,
		"consume_reservation":         consumeReservationSig,
		"consume_reservation_partial": consumeReservationPartialSig,
		"redeem_coupon":               redeemCouponSig,
		"hold_inventory":              holdInventorySig,
		"release_reservation":         releaseReservationSig,
	}

	for _, role := range []string{"store", "admin"} {
		called := productionFunctionCalls(t, role, scoped)
		for _, name := range scoped {
			want := called[name]
			got := roleHasFunctionPrivilege(t, role, signatures[name])
			if got != want {
				t.Errorf("%s execute %s = %t, want %t (production caller %t)",
					role, signatures[name], got, want, want)
			}
		}
	}
}

func productionFunctionCalls(t *testing.T, role string, names []string) map[string]bool {
	t.Helper()
	sql := generatedSQL(t)
	patterns := make([]*regexp.Regexp, len(names))
	for i, name := range names {
		patterns[i] = regexp.MustCompile(`\b` + name + `\s*\(`)
	}
	out := map[string]bool{}
	for _, pkg := range packagesOn(role) {
		for _, method := range calledQueries(t, pkg) {
			body, ok := sql[method]
			if !ok {
				continue
			}
			clean := stripSQLComments(body)
			for i, name := range names {
				if patterns[i].MatchString(clean) {
					out[name] = true
				}
			}
		}
	}
	return out
}

func roleHasFunctionPrivilege(t *testing.T, role, signature string) bool {
	t.Helper()
	var got bool
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, signature).
		Scan(&got); err != nil {
		t.Fatalf("read %s privilege on %s: %v", role, signature, err)
	}
	return got
}

// unusedTableVerbs is the leftover blanket-grant verbs on the tables named by
// #54/#55/#66. Production writes the other verb on each table; these have no
// caller.
var unusedTableVerbs = []struct {
	role, table, verb, stmt string
}{
	{"admin", "order_private_data", "INSERT",
		`INSERT INTO order_private_data (
			order_id, email, recipient_name, phone, postal_code, city, district, street)
		 VALUES (
			'6666aaaa-6666-4666-8666-666666666666', 'rewrite@example.com', '改寫',
			'0900000002', '110', '台北市', '信義區', '松高路 2 號')`},
	{"admin", "order_private_data", "DELETE",
		`DELETE FROM order_private_data WHERE order_id = '66666666-6666-4666-8666-666666666666'`},
	{"store", "contact_messages", "DELETE",
		`DELETE FROM contact_messages WHERE email = 'Ming@Example.com'`},
	{"store", "contact_messages", "UPDATE",
		`UPDATE contact_messages SET message = 'rewritten' WHERE email = 'Ming@Example.com'`},
	{"admin", "contact_messages", "DELETE",
		`DELETE FROM contact_messages WHERE email = 'Ming@Example.com'`},
	{"admin", "contact_messages", "INSERT",
		`INSERT INTO contact_messages (name, email, subject, message)
		 VALUES ('職員', 'staff@example.com', '訂單問題', '不該由後台新增')`},
	{"store", "stock_notifications", "DELETE",
		`DELETE FROM stock_notifications WHERE email = 'ming.work@example.com'`},
	{"store", "stock_notifications", "UPDATE",
		`UPDATE stock_notifications SET email = 'other@example.com'
		 WHERE email = 'ming.work@example.com'`},
	{"admin", "stock_notifications", "DELETE",
		`DELETE FROM stock_notifications WHERE email = 'ming.work@example.com'`},
	{"admin", "stock_notifications", "INSERT",
		`INSERT INTO stock_notifications (variant_id, email)
		 VALUES ('44444444-4444-4444-8444-444444444444', 'staff-restock@example.com')`},
	{"store", "checkout_attempts", "UPDATE",
		`UPDATE checkout_attempts SET order_id = '66666666-6666-4666-8666-666666666666'`},
	{"store", "warranty_registrations", "UPDATE",
		`UPDATE warranty_registrations SET serial_number = 'REWRITTEN'`},
}

var keptTableVerbs = []struct {
	role, table, verb string
}{
	{"store", "order_private_data", "INSERT"},
	{"admin", "order_private_data", "UPDATE"},
	{"store", "contact_messages", "INSERT"},
	{"admin", "contact_messages", "UPDATE"},
	{"store", "stock_notifications", "INSERT"},
	{"admin", "stock_notifications", "UPDATE"},
	{"store", "checkout_attempts", "INSERT"},
	{"store", "checkout_attempts", "DELETE"},
	{"store", "warranty_registrations", "INSERT"},
}

// TestUnusedTableVerbsStayRevoked is the verb-level lock the table-level write
// direction guard cannot see: a role that INSERTs a table still held UPDATE or
// DELETE from the blanket grant.
func TestUnusedTableVerbsStayRevoked(t *testing.T) {
	for _, door := range unusedTableVerbs {
		if roleHoldsTableVerb(t, door.role, door.table, door.verb) {
			t.Errorf("%s holds %s on %s, and no production query uses that verb",
				door.role, door.verb, door.table)
		}
	}
	for _, door := range keptTableVerbs {
		if !roleHoldsTableVerb(t, door.role, door.table, door.verb) {
			t.Errorf("%s lost %s on %s; production still writes that verb",
				door.role, door.verb, door.table)
		}
	}

	for _, door := range unusedTableVerbs {
		t.Run(door.role+"/"+door.verb+"/"+door.table, func(t *testing.T) {
			assertRoleStatementDenied(t, door.role, door.stmt)
		})
	}
}

// roleHoldsTableVerb is true when the role can perform the verb through a
// table grant or any column grant. has_table_privilege alone is blind to a
// GRANT INSERT (col) / GRANT UPDATE (col).
func roleHoldsTableVerb(t *testing.T, role, table, verb string) bool {
	t.Helper()
	if hasTablePriv(t, role, table, verb) {
		return true
	}
	if verb == "DELETE" {
		return false
	}
	var held bool
	if err := schemaPool(t).QueryRow(t.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $2
			  AND has_column_privilege($1, $2::regclass, column_name, $3)
		)`, role, table, verb).Scan(&held); err != nil {
		t.Fatalf("read %s column %s on %s: %v", role, verb, table, err)
	}
	return held
}

func assertRoleStatementDenied(t *testing.T, role, stmt string) {
	t.Helper()
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	_, execErr := tx.Exec(ctx, stmt)
	pgErr, ok := errors.AsType[*pgconn.PgError](execErr)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("%s statement failed with %v, want PgError 42501", role, execErr)
	}
}

// TestAdminContactMessageUpdateIsHandledAtOnly keeps identity and history off
// the handle/reopen door. A default on id or created_at is an INSERT exemption,
// not UPDATE authority: HandleMessage and ReopenMessage write handled_at only.
func TestAdminContactMessageUpdateIsHandledAtOnly(t *testing.T) {
	if roleHasColumnPriv(t, "admin", "contact_messages", "id", "UPDATE") {
		t.Error("admin can UPDATE contact_messages.id; handling a message does not rewrite identity")
	}
	if roleHasColumnPriv(t, "admin", "contact_messages", "created_at", "UPDATE") {
		t.Error("admin can UPDATE contact_messages.created_at; handling a message does not rewrite history")
	}
	if !roleHasColumnPriv(t, "admin", "contact_messages", "handled_at", "UPDATE") {
		t.Error("admin cannot UPDATE contact_messages.handled_at; HandleMessage and ReopenMessage write it")
	}

	ctx := t.Context()
	deny := func(name, stmt string) {
		t.Helper()
		tx, err := schemaPool(t).Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

		if _, err := tx.Exec(ctx, fixtures); err != nil {
			t.Fatalf("load fixtures: %v", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE admin"); err != nil {
			t.Fatalf("set role admin: %v", err)
		}
		_, execErr := tx.Exec(ctx, stmt)
		pgErr, ok := errors.AsType[*pgconn.PgError](execErr)
		if !ok || pgErr.Code != "42501" {
			t.Errorf("%s failed with %v, want PgError 42501", name, execErr)
		}
	}
	deny("admin UPDATE contact_messages.id",
		`UPDATE contact_messages SET id = uuidv7() WHERE email = 'Ming@Example.com'`)
	deny("admin UPDATE contact_messages.created_at",
		`UPDATE contact_messages SET created_at = now() WHERE email = 'Ming@Example.com'`)

	tx, beginErr := schemaPool(t).Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, ferr := tx.Exec(ctx, fixtures); ferr != nil {
		t.Fatalf("load fixtures: %v", ferr)
	}
	var messageID string
	if scanErr := tx.QueryRow(ctx,
		`SELECT id FROM contact_messages WHERE email = 'Ming@Example.com'`).
		Scan(&messageID); scanErr != nil {
		t.Fatalf("read fixture message: %v", scanErr)
	}
	if _, roleErr := tx.Exec(ctx, "SET LOCAL ROLE admin"); roleErr != nil {
		t.Fatalf("set role admin: %v", roleErr)
	}
	tag, handleErr := tx.Exec(ctx,
		`UPDATE contact_messages SET handled_at = now() WHERE id = $1 AND handled_at IS NULL`,
		messageID)
	if handleErr != nil {
		t.Fatalf("HandleMessage path: %v", handleErr)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("HandleMessage path updated %d rows, want 1", tag.RowsAffected())
	}
	tag, reopenErr := tx.Exec(ctx,
		`UPDATE contact_messages SET handled_at = NULL WHERE id = $1 AND handled_at IS NOT NULL`,
		messageID)
	if reopenErr != nil {
		t.Fatalf("ReopenMessage path: %v", reopenErr)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("ReopenMessage path updated %d rows, want 1", tag.RowsAffected())
	}
}

// TestStoreCannotCallRecordInventoryMovementDirectly keeps the raw stock writer
// off the storefront pool. Holds and releases reach it through narrower DEFINER
// doors; admin staff adjust and receive through the primitive on the admin pool.
func TestStoreCannotCallRecordInventoryMovementDirectly(t *testing.T) {
	assertFunctionPrivilege(t, "store", recordInventoryMovementSig, false)
	assertFunctionPrivilege(t, "admin", recordInventoryMovementSig, true)

	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE store"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	_, execErr := tx.Exec(ctx, `
		SELECT record_inventory_movement(
			'44444444-4444-4444-8444-444444444444', 999, 'receipt', 'k-store-receipt', NULL, NULL, NULL)`)
	pgErr, ok := errors.AsType[*pgconn.PgError](execErr)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("store direct record_inventory_movement failed with %v, want PgError 42501", execErr)
	}
}
