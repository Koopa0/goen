package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// touchesReviews finds any query naming the base review table, read or write.
var touchesReviews = regexp.MustCompile(`\bproduct_reviews\b`)

// TestEveryRatingReadsTheVisibleReviews holds visible_reviews as the single
// definition of a review that counts towards a rating.
func TestEveryRatingReadsTheVisibleReviews(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"HasReviewed":  "must see a hidden review, or the form offers a second one",
		"AdminReviews": "the moderation queue lists hidden reviews to un-hide them",
		"HideReview":   "moderation",
		"ShowReview":   "moderation",
		"CreateReview": "the insert",
	}

	used := map[string]bool{}
	for path, src := range queryFiles(t) {
		for _, q := range splitQueries(src) {
			if !touchesReviews.MatchString(q.body) {
				continue
			}
			if _, ok := allowed[q.name]; ok {
				used[q.name] = true
				continue
			}
			t.Errorf("%s: query %s reads product_reviews directly.\n"+
				"  Read visible_reviews instead, or a hidden review still counts "+
				"towards this product's score. If it genuinely needs the hidden "+
				"rows, name it in the allowlist with the reason.", path, q.name)
		}
	}
	// Checked by identity, not by count: a count cannot name the stale entry.
	for name, why := range allowed {
		if !used[name] {
			t.Errorf("the allowlist names %s (%s) and nothing matched it: the "+
				"query is gone or renamed, so the entry now excuses nothing. Its presence reads as coverage.", name, why)
		}
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
