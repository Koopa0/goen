//go:build integration

package restore_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func runMakeRestore(t *testing.T, copyName, addr string) ([]byte, error) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate restore package")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	work := t.TempDir()
	for _, client := range []string{"psql", "pg_dump", "pg_restore"} {
		script := "#!/bin/sh\nexec docker run --rm --network host -i " +
			"-v \"$GOEN_RESTORE_TEST_TMP:$GOEN_RESTORE_TEST_TMP\" " +
			"-v \"$GOEN_RESTORE_TEST_ROOT:$GOEN_RESTORE_TEST_ROOT:ro\" " +
			"-w \"$GOEN_RESTORE_TEST_ROOT\" " + dbtest.Image + " " + client + " \"$@\"\n"
		if writeErr := os.WriteFile(filepath.Join(work, client), []byte(script), 0o700); writeErr != nil { //nolint:gosec // G306: the owned temporary PostgreSQL client wrapper must be executable.
			t.Fatal(writeErr)
		}
	}
	cmd := exec.CommandContext(t.Context(), "make", "restore-drill")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOEN_DATABASE_URL="+pool.Config().ConnString(),
		"TMPDIR="+work, "PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOEN_RESTORE_TEST_TMP="+work, "GOEN_RESTORE_TEST_ROOT="+root,
		"GOEN_RESTORE_OBJECTIVE_SECONDS=600", "GOEN_RESTORE_COPY="+copyName, "GOEN_RESTORE_ADDR="+addr)
	return cmd.CombinedOutput()
}

func TestMakeRestorePreservesExistingDestination(t *testing.T) {
	ctx := t.Context()
	name := "restore_existing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})
	out, err := runMakeRestore(t, name, "127.0.0.1:1")
	if err == nil || !strings.Contains(string(out), "already exists") {
		t.Fatalf("destination collision: %v\n%s", err, out)
	}
	var exists bool
	if queryErr := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); queryErr != nil {
		t.Fatal(queryErr)
	}
	if !exists {
		t.Fatal("Makefile restore deleted a pre-existing destination after refusal")
	}
}

func TestMakeRestoreRejectsSourceDestination(t *testing.T) {
	out, err := runMakeRestore(t, pool.Config().ConnConfig.Database, "127.0.0.1:1")
	if err == nil || !strings.Contains(string(out), "destination equals source") {
		t.Fatalf("source collision: %v\n%s", err, out)
	}
	if pingErr := pool.Ping(t.Context()); pingErr != nil {
		t.Fatalf("source no longer accessible: %v", pingErr)
	}
}

func TestMakeRestoreRunsOwnedSourceDrill(t *testing.T) {
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	name := "restore_complete_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	out, err := runMakeRestore(t, name, addr)
	if err != nil {
		t.Fatalf("owned-source Makefile drill: %v\n%s", err, out)
	}
	for _, expected := range []string{"SCHEMA: PASS", "dump-carried GRANTs: PASS", "exact row counts: PASS", "business manifest: PASS", "authorized customer/operator HTTP views: PASS", "recovery_elapsed_s=", "recovery_objective_s=600", "restore-drill: PASS"} {
		if !strings.Contains(string(out), expected) {
			t.Errorf("drill lacks %q\n%s", expected, out)
		}
	}
	var exists bool
	if queryErr := pool.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); queryErr != nil {
		t.Fatal(queryErr)
	}
	if exists {
		t.Fatal("Makefile drill left its owned destination behind")
	}
	t.Log(string(out))
}
