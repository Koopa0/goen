package db_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	sqlLineComment = regexp.MustCompile(`(?m)--.*$`)
	// actorAlias is an output column that names a person who DID something.
	actorAlias = regexp.MustCompile(`(?is)\bAS\s+(actor|actor_name|staff)\b`)
)

// TestActorNamedByEmail holds that the back office names
// the person behind an event by their current email. users.full_name is
// whatever its owner last typed on /account/profile, so a row that named its
// actor by it would let a member of staff hand what they did to a colleague.
//
// Two shapes are refused, in every query outside migrations:
//   - a coalesce or nullif that reads full_name and email together, which is
//     how a name came to stand in for an address;
//   - an output column aliased actor, actor_name or staff that reads full_name.
//
// A name that labels the customer who wrote a review or a question is not an
// actor and is not touched by either shape. The second shape cannot see a
// column of a UNION branch that takes its name from the first branch, so such
// a column is held by the first shape alone.
func TestActorNamedByEmail(t *testing.T) {
	t.Parallel()

	inspected := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			inspected[q.name] = true
			body := sqlLineComment.ReplaceAllString(q.body, "")
			for _, call := range functionCalls(body, "coalesce", "nullif") {
				if strings.Contains(call, "full_name") && strings.Contains(call, "email") {
					t.Errorf("%s: query %s falls back from full_name to email in %q.\n"+
						"  Name the person by email alone: full_name is theirs to change.", path, q.name, squash(call))
				}
			}
			for _, loc := range actorAlias.FindAllStringIndex(body, -1) {
				if column := columnBefore(body, loc[0]); strings.Contains(column, "full_name") {
					t.Errorf("%s: query %s reads full_name into the actor column %q.\n"+
						"  Name the person by email alone: full_name is theirs to change.", path, q.name, squash(column+body[loc[0]:loc[1]]))
				}
			}
		}
	}

	// The guard is only as wide as the walk: these are the queries that name a
	// person behind an event, and each must have been inspected.
	for _, name := range []string{"AuditEvents", "AdminOrderTimeline", "OrderRefundRows", "VariantMovements"} {
		if !inspected[name] {
			t.Errorf("query %s was not inspected: it is gone or renamed, and this guard no longer covers the page it feeds", name)
		}
	}
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// functionCalls returns the text of every call to one of names, from its open
// parenthesis to the matching close, nested calls included.
func functionCalls(sql string, names ...string) []string {
	lower := strings.ToLower(sql)
	var out []string
	for _, name := range names {
		for from := 0; ; {
			i := strings.Index(lower[from:], name+"(")
			if i < 0 {
				break
			}
			start := from + i + len(name)
			from = start
			if end := matchingParen(sql, start); end > 0 {
				out = append(out, sql[start:end+1])
			}
		}
	}
	return out
}

// matchingParen is the index of the parenthesis closing the one at open, or -1.
func matchingParen(sql string, open int) int {
	depth := 0
	for i := open; i < len(sql); i++ {
		switch sql[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// columnBefore is the expression an output column is computed from, given the
// index of its AS: the text back to the comma that separates it from the
// column before, or to the parenthesis that opens the subquery it sits in.
// A comma inside a call's own parentheses does not end it.
func columnBefore(sql string, as int) string {
	depth := 0
	for i := as - 1; i >= 0; i-- {
		switch sql[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				return sql[i+1 : as]
			}
			depth--
		case ',':
			if depth == 0 {
				return sql[i+1 : as]
			}
		}
	}
	return sql[:as]
}
