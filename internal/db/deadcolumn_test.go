//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryColumnIsReadOrWritten refuses a column no SQL mentions in a statement naming its table.
func TestEveryColumnIsReadOrWritten(t *testing.T) {
	allowed := map[string]string{
		"user_identities.id": "the unbuilt OAuth sign-in, whole table",

		"email_verifications.id": "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",
		"loyalty_entries.id":     "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",
		"product_answers.id":     "a surrogate primary key; its own constraint is the use, and that is inside the block this guard cuts",

		// Listed one by one rather than exempted as a class, so a new one has to be decided here.
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

	for key, why := range allowed {
		if !exempted[key] {
			t.Errorf("the allowlist exempts %s (%s), and either SQL names it within its "+
				"own table now or no such column exists — the entry is stale and would go "+
				"on reading as a covered decision", key, why)
		}
	}
}

// sqlScope is one statement with the tables it names and the aliases it binds them to.
type sqlScope struct {
	text    string
	tables  map[string]bool
	aliases map[string]string
}

// sqlScopes is every piece of SQL this repository ships, cut into scopes. CREATE TABLE,
// COMMENT ON, GRANT and REVOKE are dropped: each is a statement ABOUT a column, not a use.
func sqlScopes(t *testing.T, tables map[string]bool) []sqlScope {
	t.Helper()

	statements := repositorySQL(t)

	// A trigger body says NEW.x and names no table: the binding comes from the CREATE TRIGGER.
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
		// Signature and inner statements judged apart, so one table cannot lend a name to another.
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

// triggerScope is a trigger function's body credited to the tables its triggers fire on.
func triggerScope(statement, body string, bound map[string]map[string]bool, tables map[string]bool) []sqlScope {
	m := functionHeader.FindStringSubmatch(statement)
	if m == nil {
		return nil
	}
	fires := bound[m[1]]
	if len(fires) == 0 {
		return nil
	}
	// Built over the whole body so the alias bindings survive, then narrowed to the trigger's table.
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

// columnIsMentioned reports whether any scope naming table mentions column in a position it owns.
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

// mentionBelongsTo reads the qualifier in front of a mention: an unqualified name counts for
// every table the scope names, a qualified one only for the table it resolves to.
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
	relationClause = regexp.MustCompile(
		`(?i)\b(?:FROM|JOIN|UPDATE|INTO|USING|TABLE)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)(?:\s+(?:AS\s+)?([a-z_][a-z0-9_]*))?`)
	// triggerBinding reads which table a trigger fires on and which function it runs.
	triggerBinding = regexp.MustCompile(
		`(?is)CREATE\s+TRIGGER\s+\w+.*?\bON\s+([a-z_][a-z0-9_]*)\b.*?EXECUTE\s+(?:FUNCTION|PROCEDURE)\s+([a-z_][a-z0-9_]*)`)
	functionHeader = regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+([a-z_][a-z0-9_]*)`)
	dollarTag      = regexp.MustCompile(`^\$[a-zA-Z_]*\$`)
	dollarBody     = regexp.MustCompile(`(?s)\$[a-zA-Z_]*\$(.*)\$[a-zA-Z_]*\$`)
)

// aliasStopWords follow a table name without being its alias: FROM orders WHERE binds no alias.
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

// sqlStatements cuts src on its top-level semicolons and drops its comments. A `--` inside a
// string literal is not a comment, and semicolons inside a $$-quoted body do not end a statement.
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
