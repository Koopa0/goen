package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// plainRollback matches a rollback handed the request's own context.
var plainRollback = regexp.MustCompile(`\btx\.Rollback\(ctx\)`)

// TestEveryRollbackOutlivesItsRequest derives its corpus from the tree rather
// than a list, so a store written next week is covered the day it is written.
//
// pgx's Rollback runs `Exec(ctx, "rollback")`, and on failure calls conn.die()
// under the comment "a rollback failure leaves the connection in an undefined
// state". A cancelled context fails that Exec before it reaches the server, so
// a rollback deferred on the request's own context rolls nothing back when the
// client disconnects: the transaction is left for the server to reap and the
// pooled connection is destroyed.
func TestEveryRollbackOutlivesItsRequest(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		//nolint:gosec // G304: path comes from WalkDir over this repository
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(src), "\n") {
			if plainRollback.MatchString(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d rollback(s) take the request's own context:\n  %s\n\n"+
			"A cancelled context makes pgx fail the ROLLBACK before it reaches the "+
			"server and destroy the pooled connection. Use "+
			"tx.Rollback(context.WithoutCancel(ctx)).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
