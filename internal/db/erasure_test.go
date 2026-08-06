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

// addressSurvivors is a table that may still hold an erased address, with the
// reason. Every entry is a claim that somebody looked.
//
// It is empty, and that is the point: there is no table in this schema where a
// forgotten customer's address is allowed to remain. An entry here would be a
// decision to keep one, which is the kind of decision that has to be argued in
// writing rather than discovered by a reader.
var addressSurvivors = map[string]string{}

// TestTheLastAdminCannotBeErased holds the door nobody was watching.
//
// /account/erase checks that the person typed their own address and then calls
// erase_user. It never consults guardLastAdmin — that guard lives in the staff
// feature, and erasure is an account feature — so the last admin could erase
// their own account and lock the shop out of its own back office permanently.
// It is the exact outcome ErrLastAdmin exists to prevent, reached through a door
// that never asked, and there is no recovery short of promoting somebody by hand
// in SQL.
//
// The guard is in erase_user rather than in the handler because CLAUDE.md
// documents that function as the ONLY door that removes a person: a second door
// added later would have to remember, and the one that forgot would be whichever
// was written next. That is how this one was missed in the first place.
//
// Bound to the CONSTRAINT NAME, not to "an error happened": a statement meant to
// prove one rule routinely trips a different one first, and a test that cannot
// tell them apart is a test that passes for the wrong reason.
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

	// Inside a SAVEPOINT, because the refusal aborts the transaction and the
	// control below has to run in the same fixtures.
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

	// The CONTROL, and it is the half that makes the test mean something: with a
	// SECOND admin present the same erasure must go through. A function that
	// refused every admin would satisfy the assertion above and quietly make the
	// shop unable to remove anybody.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, only); err != nil {
		t.Fatalf("erasing an admin who is not the last one was refused: %v", err)
	}
}

// assertNoTableHoldsTheAddress asks every table with an email column whether it
// still holds one, after erasure.
//
// # Why the question is derived rather than listed
//
// TestEraseUserLeavesNoPersonalData probed four tables by hand, and the list was
// wrong: contact_messages holds a name, an address, a subject and whatever the
// customer typed — which for "my order has not arrived" is routinely a delivery
// address and a phone number — and erase_user never touched it. /admin/messages
// reads that table, so an erased customer's own words and address stayed
// readable by the shop forever.
//
// The reasoning to catch it was already written IN erase_user, about the other
// table of the same shape: "the newsletter, which keys on the ADDRESS rather
// than the account — so a plain DELETE of a user never reaches it". It was
// applied once and not the second time. A hand-written probe list cannot notice
// that, because the missing entry is the defect.
//
// So the corpus comes from information_schema: every text column named `email`
// in a base table. A table added later is covered by existing rather than by
// somebody remembering this file, which is the same standard
// TestEveryDefinerWrittenTableIsRevoked and TestEveryColumnIsReadOrWritten are
// already held to.
//
// # What it does NOT ask
//
// Only the address, not every field that could identify somebody. A name, a
// phone number and a street are all personal data too, and they live in columns
// with no single name to derive from — order_private_data.recipient_name,
// addresses.street, contact_messages.name. The address is the one identifier
// that is spelled the same everywhere, and it is also the KEY the two missed
// tables were reachable by. Coarse and true beats precise and unwritten; this
// limit is stated rather than left for a reader to discover.
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
	// A schema with no email column anywhere means the query is wrong, not that
	// goen has stopped storing addresses.
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
