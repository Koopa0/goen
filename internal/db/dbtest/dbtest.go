//go:build integration

// Package dbtest starts a real PostgreSQL for integration tests.
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

// The schema calls uuidv7(), which is PostgreSQL 18 or later.
const image = "postgres:18-alpine"

// Pool starts a PostgreSQL container for one test and removes it afterwards.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, stop, err := Start(t.Context())
	if err != nil {
		t.Fatalf("start database: %v", err)
	}
	t.Cleanup(stop)
	return pool
}

// Start brings up PostgreSQL, applies every migration, and returns a pool and its teardown.
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
	// Glob returns sorted names; the numeric prefix is the applied order.
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

func migrationsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errNoCaller
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(root, "migrations"), nil
}
