//go:build integration

package db_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSuiteDatabaseCollation proves dbtest creates its database on
// PostgreSQL's builtin C.UTF-8 locale, which 001 requires.
func TestSuiteDatabaseCollation(t *testing.T) {
	var provider, locale string
	if err := schemaPool(t).QueryRow(t.Context(), `
		SELECT d.datlocprovider::text, coalesce(d.datlocale, '')
		FROM pg_catalog.pg_database d WHERE d.datname = current_database()
	`).Scan(&provider, &locale); err != nil {
		t.Fatalf("read the suite database's locale: %v", err)
	}
	if provider != "b" || locale != "C.UTF-8" {
		t.Errorf("suite database locale provider %q, locale %q, want %q and %q", provider, locale, "b", "C.UTF-8")
	}
}

// TestInitialSchemaLibcCollation applies 001 to a database on the
// operating-system (libc) collation and expects it to refuse.
func TestInitialSchemaLibcCollation(t *testing.T) {
	ctx := t.Context()
	admin := schemaPool(t)

	name := "goen_libc_" + strings.ToLower(rand.Text()[:12])
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER libc`); err != nil {
		t.Fatalf("create the libc database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})

	var provider string
	if err := admin.QueryRow(ctx, `SELECT datlocprovider::text FROM pg_catalog.pg_database WHERE datname = $1`, name).Scan(&provider); err != nil {
		t.Fatalf("read the new database's provider: %v", err)
	}
	if provider != "c" {
		t.Fatalf("test database provider %q, want %q (libc)", provider, "c")
	}

	cfg := admin.Config()
	cfg.ConnConfig = cfg.ConnConfig.Copy()
	cfg.ConnConfig.Database = name
	libc, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open a pool on %s: %v", name, err)
	}
	t.Cleanup(libc.Close)

	migration, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	_, err = libc.Exec(ctx, string(migration))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Fatalf("applying 001 to a libc database = %v, want a *pgconn.PgError", err)
	}
	if pgErr.Code != "55000" {
		t.Errorf("error code %q, want 55000 (object_not_in_prerequisite_state): %s", pgErr.Code, pgErr.Message)
	}
	for _, want := range []string{name, "C.UTF-8"} {
		if !strings.Contains(pgErr.Message, want) {
			t.Errorf("error message %q does not contain %q", pgErr.Message, want)
		}
	}
}
