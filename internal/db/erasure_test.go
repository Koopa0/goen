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

// addressSurvivors names any table allowed to keep an erased address, with the
// reason. It is empty.
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

	// Inside a SAVEPOINT: the refusal aborts the transaction, and the control
	// below has to run in the same fixtures.
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

	// The control: with a second admin present the same erasure must go through,
	// or a function refusing every admin would satisfy the assertion above.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, only); err != nil {
		t.Fatalf("erasing an admin who is not the last one was refused: %v", err)
	}
}

// assertNoTableHoldsTheAddress asks every base table carrying a text `email`
// column, derived from information_schema, whether it still holds one after
// erasure. Only the address: it is the one identifier spelled the same
// everywhere, and a name or a street is not asked about here.
func assertNoTableHoldsTheAddress(ctx context.Context, t *testing.T, tx pgx.Tx, addr string) {
	t.Helper()

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
	// Too few means the query above is wrong, not that goen stopped storing addresses.
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
