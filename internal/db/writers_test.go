//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryTableHasAWriter refuses a table the application never writes: a
// feature with no door. The tables come from the catalog; a writer is an
// INSERT/UPDATE/DELETE in a query.sql or inside a stored function body.
func TestEveryTableHasAWriter(t *testing.T) {
	allowed := map[string]string{}

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

// applicationSQL is every feature's query.sql. The dev seed is excluded: seeding
// a table is what a doorless feature can do and no shop can.
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

// storedFunctionBodies is every $$-quoted function body in the migration, each of
// which is a door: a SECURITY DEFINER function is the only writer of stock and the
// ledgers.
func storedFunctionBodies(t *testing.T) string {
	t.Helper()

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

// TestEveryTableIsRead is the mirror: a table the application writes and never
// reads is data collected and never shown.
func TestEveryTableIsRead(t *testing.T) {
	allowed := map[string]string{
		"payment_webhook_events": "an idempotency CLAIM: its whole purpose is the " +
			"unique index that makes at-least-once delivery safe, and the row exists " +
			"to be collided with rather than to be read",
		"order_number_counters": "read by next_order_number(), which is the only " +
			"thing that may touch it — a per-day counter under a row lock",
	}

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
		// Only tables something WRITES: one that is neither is the other guard's
		// finding, and reporting it twice makes one decision look like two problems.
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

// readsFrom reports whether the application SQL names table in a FROM or JOIN; a
// stored function reading its own bookkeeping deliberately does not count.
func readsFrom(src, table string) bool {
	return regexp.MustCompile(`(?i)(FROM|JOIN)\s+(?:ONLY\s+)?` + table + `\b`).MatchString(src)
}
