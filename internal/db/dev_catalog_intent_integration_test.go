//go:build integration

package db_test

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestDemoCatalogueRequiresExplicitDevelopmentIntent(t *testing.T) {
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, marker string }{
		{name: "missing"},
		{name: "production", marker: "production"},
		{name: "unknown", marker: "staging"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			owner := dbtest.Pool(t)
			before := tableDigests(t, owner)
			config := owner.Config().Copy()
			delete(config.ConnConfig.RuntimeParams, "options")
			if fixture.marker != "" {
				config.ConnConfig.RuntimeParams["options"] = "-c goen.catalogue_seed=" + fixture.marker
			}
			unmarked, openErr := pgxpool.NewWithConfig(t.Context(), config)
			if openErr != nil {
				t.Fatal(openErr)
			}
			t.Cleanup(unmarked.Close)
			conn, acquireErr := unmarked.Acquire(t.Context())
			if acquireErr != nil {
				t.Fatal(acquireErr)
			}
			defer conn.Release()
			_, seedErr := conn.Exec(t.Context(), string(seed))
			if _, rollbackErr := conn.Exec(t.Context(), "ROLLBACK"); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			refusal, ok := errors.AsType[*pgconn.PgError](seedErr)
			if !ok || refusal.Code != "23514" || refusal.ConstraintName != "dev_catalog_requires_development" {
				t.Fatalf("seed without development intent = %v, want named development refusal", seedErr)
			}
			if changed := changedTables(before, tableDigests(t, owner)); len(changed) != 0 {
				t.Errorf("refused development intent changed tables: %v", changed)
			}
		})
	}
}

func TestDemoCatalogueAcceptsExplicitDevelopmentIntentThroughPsql(t *testing.T) {
	shop := dbtest.Pool(t)
	if output, err := runSeed(t, "dev_catalog.sql", shop.Config().ConnString()); err != nil {
		t.Fatalf("development catalogue seed = %v; output: %s", err, output)
	}
	var products int
	if err := shop.QueryRow(t.Context(), `SELECT count(*) FROM products`).Scan(&products); err != nil {
		t.Fatal(err)
	}
	if products == 0 {
		t.Error("declared development seed left the catalogue empty")
	}
}
