//go:build integration

package queryplan

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestMeasureQueryRetainsSamplesBeforeSQLError(t *testing.T) {
	ctx := t.Context()
	pool, stop, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := pool.Exec(ctx, `CREATE SEQUENCE queryplan_sample_count`); err != nil {
		t.Fatal(err)
	}
	// The warmup and first EXPLAIN succeed; the second EXPLAIN fails in SQL.
	query := Query{
		Route: RouteHomeRecommended,
		SQL:   `SELECT 1 / (CASE WHEN nextval('queryplan_sample_count') < 3 THEN 1 ELSE 0 END)`,
	}
	results, measureErr := measureQuery(ctx, pool, ScaleSmall, query, true, "partial")
	pgErr, ok := errors.AsType[*pgconn.PgError](measureErr)
	if !ok || pgErr.Code != "22012" {
		t.Fatalf("expected the SQL failure: %v", measureErr)
	}
	if len(results) != 1 || results[0].WarmSample != 1 || len(results[0].PlanJSON) == 0 {
		t.Fatalf("successful first warm sample was lost: %+v", results)
	}
}
