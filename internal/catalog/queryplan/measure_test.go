//go:build integration

package queryplan_test

import (
	"os"
	"testing"

	"github.com/koopa0/goen/internal/catalog/queryplan"
	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestLogLargeScaleMeasurements(t *testing.T) {
	if os.Getenv("GOEN_LOG_QUERY_PLANS") != "1" {
		t.Skip("set GOEN_LOG_QUERY_PLANS=1 to log measurements")
	}
	ctx := t.Context()
	pool, stop, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if loadErr := queryplan.LoadSeed(ctx, pool, queryplan.ScaleLarge); loadErr != nil {
		t.Fatal(loadErr)
	}
	results, measureErr := queryplan.MeasureAll(ctx, pool, queryplan.ScaleLarge, "measure")
	if measureErr != nil {
		t.Fatal(measureErr)
	}
	for _, r := range results {
		t.Logf("%s %s warm=%t planning=%.2f execution=%.2f rows=%d",
			r.Scale, r.Route, r.Warm, r.PlanningMS, r.ExecutionMS, r.ActualRows)
	}
}
