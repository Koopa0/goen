//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryColumnIsReadOrWritten refuses a column nothing uses.
//
// # Why this is a test and not a review habit
//
// goen has shipped four of these, and each costs something different:
//
//   - `orders.discount_code`, declared with the table and never written — so the
//     reflex when the discount finally has to be shown is to fill it in, which is a
//     second copy of a fact one join already reaches;
//   - `users.email_verified_at`, declared and never set, with the whole feature it
//     belongs to missing underneath it: a customer who could not change their
//     address at all;
//   - `layouts.Page.CartCount`, the same defect one layer up — a view-model field
//     no handler assigns, so the badge reads 0 for every visitor with a full cart;
//   - `product_search_documents`, an entire projection TABLE with a trigram index,
//     no writer, no reader, and a note above it claiming it had exactly one writer.
//
// None of these is a compile error, none fails a test, and none is visible in a
// browser. A dead column reads to the next person as a feature — which is how it
// becomes the wrong answer to the next question.
//
// # How coverage is decided
//
// From the CATALOG, never a list: information_schema names the tables and their
// columns, and the question asked of each column is whether any SQL this repository
// ships mentions it IN A STATEMENT THAT NAMES ITS OWN TABLE. The corpus is every
// feature's query.sql, the seed, and everything in the migration that is not a
// CREATE TABLE — so a column mentioned only by its own declaration and its own
// CHECK does not count as used, which is exactly the shape the dead ones take.
//
// # The namesake, which is what makes the scoping below the whole guard
//
// Asking `\bcolumn\b` of the SQL corpus as ONE string covers a column by any OTHER
// table's column of the same name. Measured against this schema: THIRTY-ONE of 511
// columns pass on a namesake alone, a dead `note` on `order_access_grants` passes
// because `order_events.note` is written, and renaming that column `grant_note` is
// what turns it red. Seven of the thirty-one belong to the unbuilt 發票 feature
// while the allowlist below names exactly ONE such column — so an unscoped guard
// also makes its own allowlist a claim about a set it does not describe, and the
// entry that survives does so only because `tax_type` happens to be an unusual name.
// A guard that catches a dead column only when its name is distinctive catches
// almost nothing.
//
// # What ties a mention to a table
//
// The SQL is cut into SCOPES — one per statement, and a stored function's body is
// cut into its own statements as well — and a mention counts for a table only when
// its scope NAMES that table: the FROM, the JOIN, the INSERT/UPDATE/DELETE target,
// the table a CREATE INDEX, a GRANT or an ALTER TABLE is written against, or the
// table a CREATE TRIGGER binds a function to. Within a scope a qualified
// `alias.column` is resolved against that scope's own FROM/JOIN bindings, so
// `p.name` in a statement joining products and product_variants is evidence for
// products and for nothing else.
//
// # Known limits
//
// It is still a MENTION rather than a write. Telling a write from a read needs a
// SQL parser, and the failure this catches is total — a dead column is mentioned
// nowhere at all. A guard that is coarse and true beats one that is precise and
// unwritten. Where it stays coarse it is written down here, because each of these
// is a way a dead column could still hide:
//
//   - An UNQUALIFIED mention in a scope naming two tables counts for BOTH. A dead
//     `products.position` would be covered by a statement joining products and
//     product_specs that ends `ORDER BY position`.
//   - A TRIGGER FUNCTION's body is credited whole to the table its trigger fires
//     on. `NEW.x` and `OLD.x` name no table and there is nothing else to resolve
//     them against, so a column of that table mentioned anywhere in that body
//     counts.
//   - An unknown qualifier — a CTE, a PL/pgSQL record variable — counts for
//     whichever table its scope names, for the same reason.
//   - A mention inside a STRING LITERAL counts. A mention inside a COMMENT does
//     not: prose about a column is the one thing that is never a use of it, which
//     is the same reason COMMENT ON COLUMN is dropped along with CREATE TABLE.
//
// `SELECT *` names no column and would cover every column of the table it reads.
// goen writes it only into a PL/pgSQL record, never in a query, and this is the
// line that would have to be revisited if that changed.
func TestEveryColumnIsReadOrWritten(t *testing.T) {
	// Each entry is a claim that a column no SQL touches within its own table is
	// meant to exist anyway. Keyed table.column, so an exemption cannot spread.
	//
	// golang-migrate's own schema_migrations is NOT here, and the completeness
	// check below is what keeps it out: testcontainers applies 001 directly rather
	// than through the tool, so the table does not exist in the schema this asks,
	// and an entry for it is refused as stale. An allowlist entry for a column the
	// guard never sees is an entry that would go on reading as covered.
	allowed := map[string]string{
		// The OAuth link's surrogate key, and the same shape as the three below
		// it: every query on user_identities keys on (provider, provider_subject)
		// or on the user, both unique indexes are built from those columns, and
		// erase_user deletes by user — so nothing names the id anywhere this
		// corpus can see, and its only use is its own PRIMARY KEY constraint
		// inside the CREATE TABLE block this guard cuts out.
		"user_identities.id": "the unbuilt OAuth sign-in, whole table",

		// A surrogate primary key nothing has had to name. The reason it is not
		// the defect this test hunts is that it IS used — by its own PRIMARY KEY
		// constraint, which is written inside the CREATE TABLE block this guard
		// deliberately cuts out. Nothing joins to these three and no query selects
		// one, so the mention never appears anywhere the corpus can see it.
		"email_verifications.id": "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",
		"loyalty_entries.id":     "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",
		"product_answers.id":     "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",

		// Row-birth timestamps, written by their own DEFAULT now() and read by no
		// query. The DEFAULT is inside the CREATE TABLE block, which is the one
		// place this guard cannot look — so what these entries record is not "the
		// column is unwritten" but "nothing shows it to anybody", the same
		// question TestEveryTableIsRead asks one level up.
		//
		// They are listed one by one rather than exempted as a class, and that is
		// the point: a NEW table whose created_at nothing reads has to come here
		// and be decided, and any of these that starts being read retires its own
		// entry. The list is the debt, visible and counted.
		//
		"brands.created_at":                "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"carts.created_at":                 "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"categories.created_at":            "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"coupon_redemptions.created_at":    "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"faq_entries.created_at":           "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"hero_slides.created_at":           "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"membership_tiers.created_at":      "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"product_images.created_at":        "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"product_variants.created_at":      "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"products.created_at":              "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"sale_campaigns.created_at":        "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"sessions.created_at":              "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"shipping_methods.created_at":      "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"stock_notifications.created_at":   "a row-birth timestamp its own DEFAULT writes; no query shows it",
		"store_credit_accounts.created_at": "a row-birth timestamp its own DEFAULT writes; no query shows it",
	}

	ctx := t.Context()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name, c.column_name`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()

	type column struct{ table, name string }
	var columns []column
	tables := map[string]bool{}
	for rows.Next() {
		var c column
		if scanErr := rows.Scan(&c.table, &c.name); scanErr != nil {
			t.Fatalf("scan a column: %v", scanErr)
		}
		columns = append(columns, c)
		tables[c.table] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the catalog: %v", err)
	}
	if len(columns) < 200 {
		t.Fatalf("the catalog reported %d columns, want far more — the query is wrong",
			len(columns))
	}

	scopes := sqlScopes(t, tables)
	exempted := make(map[string]bool, len(allowed))
	for _, c := range columns {
		key := c.table + "." + c.name
		if columnIsMentioned(scopes, tables, c.table, c.name) {
			continue
		}
		if _, ok := allowed[key]; ok {
			exempted[key] = true
			continue
		}
		t.Errorf("%s is mentioned by no query, no seed and nothing outside its own "+
			"declaration.\n  Either something should write it — a column declared and "+
			"never written is the defect orders.discount_code and users.email_verified_at "+
			"each were — or it should be deleted. If it must exist unused, name it in the "+
			"allowlist with the reason.", key)
	}

	// By IDENTITY, never by count. A comparison of totals cannot say WHICH entry has
	// gone stale, and it passes outright when one entry goes stale as another is
	// added.
	for key, why := range allowed {
		if !exempted[key] {
			t.Errorf("the allowlist exempts %s (%s), and either SQL names it within its "+
				"own table now or no such column exists — the entry is stale and would go "+
				"on reading as a covered decision", key, why)
		}
	}
}

// sqlScope is one statement, together with the tables it names and the aliases it
// binds them to. It is the unit a column mention is judged in.
type sqlScope struct {
	text    string
	tables  map[string]bool
	aliases map[string]string
}

// sqlScopes is every piece of SQL this repository ships, cut into scopes.
//
// CREATE TABLE is dropped so a column's own declaration and its own CHECK are not
// a use of it, and COMMENT ON goes with it for the same reason: both are the
// column being described rather than read. Everything else stays — a stored
// function body, a trigger's WHEN clause and a DEFAULT expression in an ALTER
// are all real users of a column.
//
// GRANT and REVOKE are dropped too, and that exclusion carries the whole guard
// on the eight tables the privilege model narrows column by column.
// `GRANT INSERT (id, product_id, …)` names every column of a table, so counting
// one would make every column of those tables read as USED — the guard would go
// blind on exactly the surface somebody took the trouble to narrow, and the only
// visible symptom is oblique: allowlist entries for product_answers.id and
// stock_notifications.created_at start reporting themselves stale, because those
// columns are mentioned by NOTHING ELSE and a grant is a mention.
//
// A privilege list is a statement ABOUT a column, in the same category as its
// declaration and its comment: it says who may write it, not that anybody does.
// Counting it is how a guard keeps passing while its subject disappears — the
// same shape as the DEFAULT filter that silences `users.role` in the column
// guard next door.
func sqlScopes(t *testing.T, tables map[string]bool) []sqlScope {
	t.Helper()

	statements := repositorySQL(t)

	// A trigger function's body says NEW.x and OLD.x and names no table, so the
	// binding has to come from the CREATE TRIGGER that installs it.
	bound := map[string]map[string]bool{}
	for _, s := range statements {
		for _, m := range triggerBinding.FindAllStringSubmatch(s, -1) {
			if bound[m[2]] == nil {
				bound[m[2]] = map[string]bool{}
			}
			bound[m[2]][m[1]] = true
		}
	}

	var scopes []sqlScope
	for _, s := range statements {
		head := strings.ToUpper(strings.TrimSpace(s))
		if strings.HasPrefix(head, "CREATE TABLE") || strings.HasPrefix(head, "COMMENT ON") ||
			strings.HasPrefix(head, "GRANT ") || strings.HasPrefix(head, "REVOKE ") {
			continue
		}
		body := ""
		if m := dollarBody.FindStringSubmatch(s); m != nil {
			body = m[1]
		}
		if body == "" {
			scopes = append(scopes, newSQLScope(s, tables))
			continue
		}
		// The signature and its inner statements are judged separately, so a
		// function touching several tables cannot lend one table's column name to
		// another's statement — which is how erase_user would cover a dead
		// `order_access_grants.note` with `order_events.note`.
		scopes = append(scopes, newSQLScope(strings.Replace(s, body, "", 1), tables))
		for _, inner := range sqlStatements(body) {
			scopes = append(scopes, newSQLScope(inner, tables))
		}
		scopes = append(scopes, triggerScope(s, body, bound, tables)...)
	}
	if len(scopes) < 500 {
		t.Fatalf("the SQL split into %d scopes, want far more — the walk found nothing",
			len(scopes))
	}
	return scopes
}

// triggerScope is the whole body of a trigger function, credited to the tables its
// triggers fire on. Nothing else can resolve NEW and OLD.
func triggerScope(statement, body string, bound map[string]map[string]bool, tables map[string]bool) []sqlScope {
	m := functionHeader.FindStringSubmatch(statement)
	if m == nil {
		return nil
	}
	fires := bound[m[1]]
	if len(fires) == 0 {
		return nil
	}
	// Built over the whole body so the alias bindings survive — a `p.status` inside
	// it still resolves to payments — and then narrowed to the trigger's own table,
	// which is the only thing NEW and OLD can mean.
	sc := newSQLScope(body, tables)
	sc.tables = fires
	return []sqlScope{sc}
}

// newSQLScope reads which of tables a statement names, and what it calls them.
func newSQLScope(text string, tables map[string]bool) sqlScope {
	sc := sqlScope{text: text, tables: map[string]bool{}, aliases: map[string]string{}}
	for table := range tables {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(table) + `\b`).MatchString(text) {
			sc.tables[table] = true
		}
	}
	for _, m := range relationClause.FindAllStringSubmatch(text, -1) {
		table := strings.ToLower(m[1])
		if !tables[table] {
			continue
		}
		if alias := strings.ToLower(m[2]); alias != "" && !aliasStopWords[alias] {
			sc.aliases[alias] = table
		}
	}
	return sc
}

// columnIsMentioned reports whether any scope naming table mentions column in a
// position that could be that table's.
func columnIsMentioned(scopes []sqlScope, tables map[string]bool, table, column string) bool {
	mention := regexp.MustCompile(`\b` + regexp.QuoteMeta(column) + `\b`)
	for _, sc := range scopes {
		if !sc.tables[table] {
			continue
		}
		for _, at := range mention.FindAllStringIndex(sc.text, -1) {
			if mentionBelongsTo(sc, tables, table, at[0]) {
				return true
			}
		}
	}
	return false
}

// mentionBelongsTo reads the qualifier in front of a mention.
//
// An unqualified name could be any table the scope names, so it counts. A
// qualified one counts only when the qualifier resolves to this table — or to
// nothing the scope can resolve at all, which is NEW, OLD, a CTE or a record
// variable, and those are the coarse cases the doc comment names.
func mentionBelongsTo(sc sqlScope, tables map[string]bool, table string, at int) bool {
	if at == 0 || sc.text[at-1] != '.' {
		return true
	}
	start := at - 1
	for start > 0 && isIdentifierByte(sc.text[start-1]) {
		start--
	}
	qualifier := strings.ToLower(sc.text[start : at-1])
	if bound, ok := sc.aliases[qualifier]; ok {
		return bound == table
	}
	if tables[qualifier] {
		return qualifier == table
	}
	return true
}

var (
	// relationClause finds a table and the alias, if any, the statement gives it.
	relationClause = regexp.MustCompile(
		`(?i)\b(?:FROM|JOIN|UPDATE|INTO|USING|TABLE)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)(?:\s+(?:AS\s+)?([a-z_][a-z0-9_]*))?`)
	// triggerBinding reads which table a trigger fires on and which function it runs.
	triggerBinding = regexp.MustCompile(
		`(?is)CREATE\s+TRIGGER\s+\w+.*?\bON\s+([a-z_][a-z0-9_]*)\b.*?EXECUTE\s+(?:FUNCTION|PROCEDURE)\s+([a-z_][a-z0-9_]*)`)
	functionHeader = regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+([a-z_][a-z0-9_]*)`)
	dollarTag      = regexp.MustCompile(`^\$[a-zA-Z_]*\$`)
	dollarBody     = regexp.MustCompile(`(?s)\$[a-zA-Z_]*\$(.*)\$[a-zA-Z_]*\$`)
)

// aliasStopWords are the words that follow a table name without being its alias.
// Binding one would make `FROM orders WHERE ...` call the table "where".
var aliasStopWords = map[string]bool{
	"where": true, "set": true, "on": true, "using": true, "values": true,
	"select": true, "left": true, "right": true, "inner": true, "outer": true,
	"full": true, "cross": true, "join": true, "lateral": true, "order": true,
	"group": true, "having": true, "limit": true, "offset": true, "union": true,
	"returning": true, "and": true, "or": true, "for": true, "with": true,
	"as": true, "into": true, "from": true, "loop": true, "if": true, "then": true,
}

// repositorySQL is every statement in every .sql file this repository ships.
func repositorySQL(t *testing.T) []string {
	t.Helper()

	var out []string
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".sql") || strings.HasSuffix(path, ".down.sql") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		out = append(out, sqlStatements(string(src))...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk for SQL: %v", err)
	}
	return out
}

// sqlStatements cuts src on its top-level semicolons and drops its comments.
//
// It is quote-aware in both directions and both directions matter: a `--` inside a
// string literal is not a comment, and the semicolons inside a $$-quoted function
// body do not end a statement.
func sqlStatements(src string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "--"):
			i = sqlLineEnd(src, i)
		case strings.HasPrefix(src[i:], "/*"):
			i = sqlBlockEnd(src, i)
		case src[i] == '\'':
			end := sqlQuoteEnd(src, i)
			b.WriteString(src[i:end])
			i = end
		case src[i] == '$' && dollarTag.MatchString(src[i:]):
			end := sqlDollarEnd(src, i)
			b.WriteString(src[i:end])
			i = end
		case src[i] == ';':
			out = append(out, b.String())
			b.Reset()
			i++
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func sqlLineEnd(src string, i int) int {
	if end := strings.IndexByte(src[i:], '\n'); end >= 0 {
		return i + end
	}
	return len(src)
}

func sqlBlockEnd(src string, i int) int {
	if end := strings.Index(src[i+2:], "*/"); end >= 0 {
		return i + 2 + end + 2
	}
	return len(src)
}

// sqlQuoteEnd walks past a '...' literal, where ” is an escaped quote.
func sqlQuoteEnd(src string, i int) int {
	for j := i + 1; j < len(src); j++ {
		if src[j] != '\'' {
			continue
		}
		if j+1 < len(src) && src[j+1] == '\'' {
			j++
			continue
		}
		return j + 1
	}
	return len(src)
}

// sqlDollarEnd walks past a $tag$...$tag$ body.
func sqlDollarEnd(src string, i int) int {
	tag := dollarTag.FindString(src[i:])
	end := strings.Index(src[i+len(tag):], tag)
	if end < 0 {
		return len(src)
	}
	return i + len(tag) + end + len(tag)
}

func isIdentifierByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
