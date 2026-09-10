//go:build integration

package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// addressSurvivors names any table allowed to keep an erased address, with the reason.
var addressSurvivors = map[string]string{}

// TestTheLastAdminCannotBeErased holds erase_user to refusing the only admin.
func TestTheLastAdminCannotBeErased(t *testing.T) {
	ctx := t.Context()
	tx, beginErr := schemaPool(t).Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	const only = "55555555-5555-4555-8555-555555555555"
	const second = "5555aaaa-5555-4555-8555-555555555555"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, only); err != nil {
		t.Fatalf("make an admin: %v", err)
	}

	// Inside a SAVEPOINT: the refusal aborts the transaction, and the control below needs these fixtures.
	if _, err := tx.Exec(ctx, `SAVEPOINT last_admin`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, eraseErr := tx.Exec(ctx, `SELECT erase_user($1)`, only)
	if pgErr, ok := errors.AsType[*pgconn.PgError](eraseErr); !ok ||
		pgErr.ConstraintName != "erase_user_keeps_one_admin" {
		t.Fatalf("erasing the last admin = %v, want the erase_user_keeps_one_admin refusal", eraseErr)
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT last_admin`); err != nil {
		t.Fatalf("roll back to the savepoint: %v", err)
	}

	// The control: a function refusing EVERY admin would satisfy the assertion above.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, only); err != nil {
		t.Fatalf("erasing an admin who is not the last one was refused: %v", err)
	}
}

// TestUsersTriggerKeepsOneAdmin proves the database-wide fallback, independent
// of the application functions: even an owner-issued raw role update or delete
// cannot remove the only administrator.
func TestUsersTriggerKeepsOneAdmin(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	const only = "55555555-5555-4555-8555-555555555555"
	const second = "5555aaaa-5555-4555-8555-555555555555"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, only); err != nil {
		t.Fatalf("make an admin: %v", err)
	}

	for name, statement := range map[string]string{
		"role":   `UPDATE users SET role = 'staff' WHERE id = '` + only + `'`,
		"delete": `DELETE FROM users WHERE id = '` + only + `'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tx.Exec(ctx, `SAVEPOINT trigger_guard`); err != nil {
				t.Fatalf("savepoint: %v", err)
			}
			_, transitionErr := tx.Exec(ctx, statement)
			if pgErr, ok := errors.AsType[*pgconn.PgError](transitionErr); !ok ||
				pgErr.ConstraintName != "users_keep_one_admin" {
				t.Fatalf("last-admin %s = %v, want users_keep_one_admin", name, transitionErr)
			}
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT trigger_guard`); err != nil {
				t.Fatalf("rollback refusal: %v", err)
			}
		})
	}

	// A second admin is the neighbouring legal state; the trigger is not a ban
	// on every roster change.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, only); err != nil {
		t.Fatalf("demote with a surviving admin: %v", err)
	}
}

// assertNoTableHoldsTheAddress asks every table with a text email column, derived from
// information_schema, whether it still holds the address after erasure.
func assertNoTableHoldsTheAddress(ctx context.Context, t *testing.T, tx pgx.Tx, addr string) {
	t.Helper()
	assertNoJSONHoldsTheAddress(ctx, t, tx, addr)

	rows, err := tx.Query(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		  AND c.column_name = 'email' AND c.data_type = 'text'
		ORDER BY c.table_name`)
	if err != nil {
		t.Fatalf("find the tables holding an address: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan a table name: %v", scanErr)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the tables holding an address: %v", err)
	}
	if len(tables) < 4 {
		t.Fatalf("only %d tables carry an email column, want more — the query is wrong",
			len(tables))
	}

	found := 0
	for _, table := range tables {
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE lower(email) = lower($1)`,
			pgx.Identifier{table}.Sanitize()), addr).Scan(&n); err != nil {
			t.Fatalf("probe %s for the erased address: %v", table, err)
		}
		if n == 0 {
			continue
		}
		if why, ok := addressSurvivors[table]; ok {
			found++
			t.Logf("%s keeps the address: %s", table, why)
			continue
		}
		t.Errorf("%s still holds the erased address after erase_user (%d row(s)).\n"+
			"  If it keys on the ADDRESS rather than on the user, a DELETE of the "+
			"account never reaches it — which is how contact_messages kept a "+
			"customer's own words, address and phone number after they asked to be "+
			"forgotten, and how the newsletter would have gone on emailing them.\n"+
			"  Either erase_user should reach it, or it belongs in addressSurvivors "+
			"with the reason.", table, n)
	}
	if found < len(addressSurvivors) {
		t.Errorf("%d of the %d addressSurvivors entries actually hold the address; the "+
			"rest are stale entries claiming a gap that has been closed",
			found, len(addressSurvivors))
	}
}

// assertNoJSONHoldsTheAddress is the half a column sweep cannot see: an address
// inside a jsonb payload sits under no column named `email`. goen has two such
// stores — the outbox freezes the recipient into every message it enqueues, and
// audit_events is append-only with erase_user unable to reach it at all.
//
// Derived from information_schema like its neighbour, so a third jsonb store is
// covered the day it is added.
func assertNoJSONHoldsTheAddress(ctx context.Context, t *testing.T, tx pgx.Tx, addr string) {
	t.Helper()

	rows, err := tx.Query(ctx, `
		SELECT c.table_name, c.column_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		  AND c.data_type IN ('jsonb', 'json')
		ORDER BY c.table_name, c.column_name`)
	if err != nil {
		t.Fatalf("find the jsonb columns: %v", err)
	}
	type col struct{ table, name string }
	var cols []col
	for rows.Next() {
		var c col
		if scanErr := rows.Scan(&c.table, &c.name); scanErr != nil {
			t.Fatalf("scan a jsonb column: %v", scanErr)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the jsonb columns: %v", err)
	}
	if len(cols) < 2 {
		t.Fatalf("only %d jsonb columns found, want at least the outbox payload and "+
			"the audit trail — the query is wrong and this proved nothing", len(cols))
	}

	// The whole value as text, not payload->>'email': an address can sit at any
	// key, and the point is that it is GONE rather than gone from one field.
	for _, c := range cols {
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE %s::text ILIKE '%%' || $1 || '%%'`,
			pgx.Identifier{c.table}.Sanitize(),
			pgx.Identifier{c.name}.Sanitize()), addr).Scan(&n); err != nil {
			t.Fatalf("probe %s.%s for the erased address: %v", c.table, c.name, err)
		}
		if n > 0 {
			t.Errorf("%s.%s still holds the erased address in %d row(s) — the privacy "+
				"page promises it is deleted, and JSON is where a column sweep does not look",
				c.table, c.name, n)
		}
	}
}
