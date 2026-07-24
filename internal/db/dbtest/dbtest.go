//go:build integration

// Package dbtest starts a real PostgreSQL for integration tests.
//
// The database is never mocked. A mock would agree with whatever the code
// believes, which is exactly the thing under test: goen's schema carries a
// large part of its correctness in CHECK constraints, partial unique indexes
// and foreign keys, and none of those exist in a fake.
package dbtest

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// image is pinned to the same major version the application runs against.
// PostgreSQL 18 is not incidental here: the schema calls uuidv7().
const image = "postgres:18-alpine"

// Pool starts a PostgreSQL container for one test and removes it afterwards.
// A package whose tests share a database should call [Start] from TestMain
// instead: a container per test function costs several seconds each.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, stop, err := Start(t.Context())
	if err != nil {
		t.Fatalf("start database: %v", err)
	}
	t.Cleanup(stop)
	return pool
}

// Start brings up PostgreSQL, applies every migration, and returns a pool
// together with the function that tears it down. The caller owns the lifetime,
// which is what lets TestMain keep one container for a whole package.
func Start(ctx context.Context) (*pgxpool.Pool, func(), error) {
	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("goen_test"),
		postgres.WithUsername("goen"),
		postgres.WithPassword("goen"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("run postgres: %w", err)
	}
	terminate := func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			slog.Warn("dbtest: terminate postgres", "error", terr)
		}
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("open pool: %w", err)
	}

	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		terminate()
		return nil, nil, err
	}

	return pool, func() {
		pool.Close()
		terminate()
	}, nil
}

// migrate applies the repository's up migrations in order. It reads the same
// files golang-migrate runs in development, so a test cannot pass against a
// schema the deployment would not get.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	dir, err := migrationsDir()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errNoMigrations
	}
	// Glob returns sorted names, and the numeric prefix makes that the applied
	// order.
	for _, file := range files {
		sql, err := readFile(file)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, sql); err != nil {
			return migrationError{file: filepath.Base(file), err: err}
		}
	}
	return nil
}

// migrationsDir locates migrations/ from this source file's own path, so the
// helper works regardless of which package's tests are running.
func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errNoCaller
	}
	// internal/db/dbtest/dbtest.go -> repository root
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "migrations"), nil
}
