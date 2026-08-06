package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// touchesReviews finds any query naming the base review table, read or write.
//
// Writes as well as reads, so the allowlist documents every query that touches
// it rather than only the ones that could get the score wrong. A new INSERT is
// then a deliberate entry rather than an omission nobody sees.
var touchesReviews = regexp.MustCompile(`\bproduct_reviews\b`)

// TestEveryRatingReadsTheVisibleReviews holds the single definition of a review
// that counts.
//
// The displayed rating is computed LIVE from these rows in eleven queries across
// five packages — the product page, the listing, search, the home page, the
// account. A hidden review excluded from some of them means the same product
// shows two different scores, and the one that forgot would be whichever query
// was written next.
//
// So visible_reviews is the single definition, the way committed_orders is, and
// this is what keeps it that way. Two callers are allowed the base table and
// both are named with their reason: moderation has to see hidden rows to
// un-hide them, and HasReviewed has to see them or the customer is offered a
// second review and meets the unique index.
func TestEveryRatingReadsTheVisibleReviews(t *testing.T) {
	t.Parallel()

	// Keyed on the QUERY NAME, so the exception survives the SQL being
	// reformatted and does not quietly cover a new query in the same file.
	allowed := map[string]string{
		"HasReviewed":  "must see a hidden review, or the form offers a second one",
		"AdminReviews": "the moderation queue lists hidden reviews to un-hide them",
		"HideReview":   "moderation",
		"ShowReview":   "moderation",
		"CreateReview": "the insert",
	}

	found := 0
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			if !touchesReviews.MatchString(q.body) {
				continue
			}
			found++
			if _, ok := allowed[q.name]; ok {
				continue
			}
			t.Errorf("%s: query %s reads product_reviews directly.\n"+
				"  Read visible_reviews instead, or a hidden review still counts "+
				"towards this product's score. If it genuinely needs the hidden "+
				"rows, name it in the allowlist with the reason.", path, q.name)
		}
	}
	if found < len(allowed) {
		t.Fatalf("found %d queries touching product_reviews and the allowlist names "+
			"%d — the parser stopped matching", found, len(allowed))
	}
}

// namedQuery is one sqlc query: its name and the SQL beneath it.
type namedQuery struct {
	name string
	body string
}

// splitQueries carves a .sql file at its `-- name:` markers.
func splitQueries(src string) []namedQuery {
	marker := regexp.MustCompile(`(?m)^--\s*name:\s*(\w+)`)
	locs := marker.FindAllStringSubmatchIndex(src, -1)
	out := make([]namedQuery, 0, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, namedQuery{
			name: src[loc[2]:loc[3]],
			body: src[loc[1]:end],
		})
	}
	return out
}

// queryFiles is every feature's query.sql.
func queryFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".sql") || strings.Contains(path, "migrations") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		out[path] = string(src)
		return nil
	})
	if err != nil {
		t.Fatalf("walk for queries: %v", err)
	}
	return out
}
