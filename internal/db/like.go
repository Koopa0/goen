package db

import "strings"

// likeEscaper covers the three characters LIKE and ILIKE read as syntax; the
// backslash is PostgreSQL's default escape character.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike makes typed words match only themselves in a LIKE or ILIKE
// pattern: without it a typed % or _ is a wildcard, and "%%" matches every row.
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}
