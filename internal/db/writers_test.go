//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryTableHasAWriter refuses a feature with no door.
//
// # What this catches, which nothing else did
//
// A table the application READS and never WRITES is machinery with no entrance: the
// feature looks finished from the outside, the schema documents it, CLAUDE.md may
// even promise it — and the only way to put a row in is SQL. goen has shipped five:
//
//   - product_specs, so /compare showed two empty columns for every product the
//     shop created itself;
//   - product_options and its values, so the variant picker had nothing to pick and
//     every variant after the first was unreachable;
//   - promo_banners, a documented site-wide feature whose fixture in check-layout
//     had to reach past the application with psql — which is the tell;
//   - faq_entries, under a line in CLAUDE.md claiming support could answer a
//     question "without a deploy";
//   - shipping_methods and shipping_zones, so a shop could change its prices and
//     not its carriers.
//
// Each was found by running this question by hand. Asking it in CI is the difference
// between a habit and a guarantee.
//
// # How coverage is decided
//
// The tables come from the CATALOG. A writer is an INSERT/UPDATE/DELETE naming the
// table in any query.sql, or inside a stored function body — the SECURITY DEFINER
// functions are the only door to money, stock and the ledgers, and that is a door.
// The dev seed does NOT count: seeding a table is precisely what these five could do
// and no shop could.
func TestEveryTableHasAWriter(t *testing.T) {
	// Each entry is a claim that a table with no application writer is meant to have
	// none — a decision, not a gap.
	//
	// EMPTY, and that is the point rather than an oversight. It held five when
	// this guard was written and three for a long time after; the last of them —
	// invoice_documents, its lines, and user_identities — went when the 加值中心
	// and Google integrations arrived. Every table in this schema now has a door.
	// The map stays because the next table to be added before its feature is the
	// one this catches.
	allowed := map[string]string{}
	// golang-migrate's schema_migrations is NOT here, and it was — refused for the
	// same reason TestEveryColumnIsReadOrWritten refused it: testcontainers applies
	// 001 directly rather than through the tool, so the table is not in the schema
	// this asks. Twice now, which is the completeness check paying for itself.

	ctx := t.Context()
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan a table: %v", scanErr)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the catalog: %v", err)
	}
	if len(tables) < 40 {
		t.Fatalf("the catalog reported %d tables, want far more — the query is wrong",
			len(tables))
	}

	app := applicationSQL(t)
	definers := storedFunctionBodies(t)
	used := map[string]bool{}
	for _, table := range tables {
		if writesTo(app, table) || writesTo(definers, table) {
			continue
		}
		if _, ok := allowed[table]; ok {
			used[table] = true
			continue
		}
		t.Errorf("no application query and no stored function writes %s.\n"+
			"  If the site READS it, this is a feature with no door: the schema "+
			"describes it, and the only way to put a row in is SQL. Build the door, "+
			"or name the table in the allowlist with the reason it has none.", table)
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and something writes it now — "+
				"the entry is stale", name, why)
		}
	}
}

// writesTo reports whether src contains an INSERT, UPDATE or DELETE naming table.
//
// A MENTION in a write statement rather than a parse: telling a real write from a
// coincidence needs a SQL parser, and the failure this catches is total — the tables
// it finds are named in no write statement anywhere.
func writesTo(src, table string) bool {
	for _, pattern := range []string{
		`INSERT\s+INTO\s+` + table + `\b`,
		`UPDATE\s+(?:ONLY\s+)?` + table + `\b`,
		`DELETE\s+FROM\s+` + table + `\b`,
	} {
		if regexp.MustCompile(`(?i)` + pattern).MatchString(src) {
			return true
		}
	}
	return false
}

// applicationSQL is every feature's query.sql, and nothing else.
//
// The dev seed is deliberately excluded: seeding a table is exactly what the five
// doorless features could do and no shop could, so counting it would make this test
// agree that they were finished.
func applicationSQL(t *testing.T) string {
	t.Helper()

	var b strings.Builder
	for path, src := range queryFiles(t) {
		if strings.Contains(path, "seed") {
			continue
		}
		b.WriteString(src)
		b.WriteString("\n")
	}
	if b.Len() < 10000 {
		t.Fatalf("the application SQL is %d bytes, want far more", b.Len())
	}
	return b.String()
}

// storedFunctionBodies is every $$-quoted function body in the migration.
//
// A SECURITY DEFINER function IS a door — record_inventory_movement and
// post_store_credit are the only way to write stock and the ledger at all, which is
// the point of them.
func storedFunctionBodies(t *testing.T) string {
	t.Helper()

	// Read directly: queryFiles deliberately skips migrations, because every other
	// guard over it asks about the application's own SQL.
	src, err := os.ReadFile(filepath.Join("..", "..", "migrations",
		"001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	bodies := regexp.MustCompile(`(?s)\$\$(.*?)\$\$`).FindAllString(string(src), -1)
	if len(bodies) < 10 {
		t.Fatalf("found %d stored function bodies, want far more", len(bodies))
	}
	return strings.Join(bodies, "\n")
}

// TestEveryTableIsRead is the mirror question, and it found two.
//
// A table the application WRITES and never READS is data collected and never shown —
// a feature with no door from the other side, and just as invisible. goen had two:
//
//   - inventory_movements, the stock ledger every change goes through. A shop could
//     see that a SKU holds four units and not how it got there;
//   - invoice_preferences, the 發票 choice collected at checkout, so a staff member
//     packing an order could not see whether it needed a 統編 invoice.
//
// Neither failed anything. Both looked finished: the write was there, the schema
// documented the shape, and the reading half simply did not exist.
func TestEveryTableIsRead(t *testing.T) {
	allowed := map[string]string{
		"payment_webhook_events": "an idempotency CLAIM: its whole purpose is the " +
			"unique index that makes at-least-once delivery safe, and the row exists " +
			"to be collided with rather than to be read",
		"order_number_counters": "read by next_order_number(), which is the only " +
			"thing that may touch it — a per-day counter under a row lock",
	}
	// The unbuilt 發票 tables and user_identities are NOT here. Nothing writes them
	// either, which makes them TestEveryTableHasAWriter's finding — and this guard
	// skips a table nothing writes, so an entry for one is stale by construction.
	// Reporting the same table from both would make one decision look like two
	// problems. The completeness check said so on the first run.

	ctx := t.Context()
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan a table: %v", scanErr)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the catalog: %v", err)
	}

	app := applicationSQL(t)
	definers := storedFunctionBodies(t)
	used := map[string]bool{}
	for _, table := range tables {
		// Only tables something WRITES. One that is neither read nor written is the
		// other guard's finding, and reporting it twice would make each look like
		// two problems.
		if !writesTo(app, table) && !writesTo(definers, table) {
			continue
		}
		if readsFrom(app, table) {
			continue
		}
		if _, ok := allowed[table]; ok {
			used[table] = true
			continue
		}
		t.Errorf("the application writes %s and never reads it.\n"+
			"  Data collected and never shown is a feature with no door from the "+
			"other side: the write is there, the schema documents the shape, and "+
			"nobody can see the result. Show it, or name the table in the allowlist "+
			"with the reason nobody needs to.", table)
	}
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist exempts %q (%s) and something reads it now, or "+
				"nothing writes it — the entry is stale", name, why)
		}
	}
}

// readsFrom reports whether src names table in a FROM or JOIN.
//
// A stored function reading its own bookkeeping does not count, deliberately:
// next_order_number reads order_number_counters and that tells a shop nothing. The
// question here is whether the APPLICATION can show what it collected.
func readsFrom(src, table string) bool {
	return regexp.MustCompile(`(?i)(FROM|JOIN)\s+(?:ONLY\s+)?` + table + `\b`).MatchString(src)
}
