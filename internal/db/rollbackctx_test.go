package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// anyRollback matches every rollback call, whatever the receiver is named. A
// savepoint is the same pgx Tx as the outer transaction; a cancelled context
// still kills the pooled connection.
var anyRollback = regexp.MustCompile(`\.Rollback\(`)

// detachedRollback is the one argument that outlives the request.
var detachedRollback = regexp.MustCompile(`\.Rollback\(context\.WithoutCancel\(`)

// requestBoundRollback reports a rollback on this line whose argument is
// anything but context.WithoutCancel(...): the request's own context under any
// name, r.Context(), a stored context or a helper's result.
func requestBoundRollback(line string) bool {
	return len(anyRollback.FindAllStringIndex(line, -1)) >
		len(detachedRollback.FindAllStringIndex(line, -1))
}

func TestTheRollbackRuleSeesWhatTheOldPatternMissed(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`defer func() { _ = tx.Rollback(ctx) }()`:                              true,
		`defer func() { _ = tx.Rollback(r.Context()) }()`:                      true,
		`defer func() { _ = tx.Rollback(cleanupCtx) }()`:                       true,
		`_ = sp.Rollback(c)`:                                                   true,
		`defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()`:       false,
		`_ = a.Rollback(context.WithoutCancel(ctx)); _ = b.Rollback(ctx)`:      true,
		`rollbackAfter := context.WithoutCancel(ctx) // no rollback call here`: false,
	} {
		if got := requestBoundRollback(line); got != want {
			t.Errorf("requestBoundRollback(%q) = %v, want %v", line, got, want)
		}
	}
}

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
			if requestBoundRollback(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d rollback(s) take something other than a detached context:\n  %s\n\n"+
			"A request-bound context makes pgx fail the ROLLBACK before it reaches the "+
			"server and destroy the pooled connection. Use "+
			".Rollback(context.WithoutCancel(ctx)).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
