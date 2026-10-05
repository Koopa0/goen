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

// boundedRollback is the one call that outlives the request and still ends.
var boundedRollback = regexp.MustCompile(`\bpgtx\.Rollback\(`)

// rollbackHelper is the one file that calls a transaction's Rollback itself.
var rollbackHelper = filepath.Join("internal", "pgtx", "pgtx.go")

// unboundedRollback reports a rollback on this line that does not go through
// pgtx.Rollback: the request's own context under any name, r.Context(), a
// stored context, a helper's result, or context.WithoutCancel with no deadline.
func unboundedRollback(line string) bool {
	return len(anyRollback.FindAllStringIndex(line, -1)) >
		len(boundedRollback.FindAllStringIndex(line, -1))
}

func TestTheRollbackRuleSeesWhatTheOldPatternMissed(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`defer func() { _ = tx.Rollback(ctx) }()`:                              true,
		`defer func() { _ = tx.Rollback(r.Context()) }()`:                      true,
		`defer func() { _ = tx.Rollback(cleanupCtx) }()`:                       true,
		`_ = sp.Rollback(c)`:                                                   true,
		`defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()`:       true,
		`defer pgtx.Rollback(ctx, tx)`:                                         false,
		`pgtx.Rollback(ctx, sp)`:                                               false,
		`pgtx.Rollback(ctx, a); _ = b.Rollback(context.WithoutCancel(ctx))`:    true,
		`rollbackAfter := context.WithoutCancel(ctx) // no rollback call here`: false,
	} {
		if got := unboundedRollback(line); got != want {
			t.Errorf("unboundedRollback(%q) = %v, want %v", line, got, want)
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
// pooled connection is destroyed. Detached with no deadline instead, a ROLLBACK
// to a server that stopped answering holds its goroutine and connection until
// TCP gives up. pgtx.Rollback is detached and bounded.
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
		if rel == rollbackHelper {
			return nil
		}
		for i, line := range strings.Split(string(src), "\n") {
			if unboundedRollback(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d rollback(s) do not go through pgtx.Rollback:\n  %s\n\n"+
			"A request-bound context makes pgx fail the ROLLBACK before it reaches the "+
			"server and destroy the pooled connection; a detached one with no deadline "+
			"waits on a silent server for as long as TCP does. Use "+
			"defer pgtx.Rollback(ctx, tx).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}
