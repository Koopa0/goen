package queryplan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Result is one EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) execution.
type Result struct {
	Route         Route
	Scale         Scale
	Warm          bool
	WarmSample    int // 0 = cold; 1..warmSamples = warm repeat index
	PlanningMS    float64
	ExecutionMS   float64
	ActualRows    int64
	CountRead     bool
	CommitSHA     string
	MeasuredAtUTC time.Time
	PlanJSON      json.RawMessage
}

type explainRoot struct {
	Plan struct {
		ActualRows float64 `json:"Actual Rows"`
	} `json:"Plan"`
	PlanningTime  float64 `json:"Planning Time"`
	ExecutionTime float64 `json:"Execution Time"`
}

// Measure runs one query with EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) and
// returns parsed timing and row cardinalities.
func Measure(ctx context.Context, pool *pgxpool.Pool, q Query, scale Scale, warm bool, warmSample int, sha string) (Result, error) {
	explainSQL := "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + q.SQL
	var payload []byte
	if err := pool.QueryRow(ctx, explainSQL, q.Args...).Scan(&payload); err != nil {
		return Result{}, fmt.Errorf("%s: %w", q.Route, err)
	}
	var roots []explainRoot
	if err := json.Unmarshal(payload, &roots); err != nil {
		return Result{}, fmt.Errorf("%s: parse explain json: %w", q.Route, err)
	}
	if len(roots) == 0 {
		return Result{}, fmt.Errorf("%s: empty explain payload", q.Route)
	}
	root := roots[0]
	return Result{
		Route:         q.Route,
		Scale:         scale,
		Warm:          warm,
		WarmSample:    warmSample,
		PlanningMS:    root.PlanningTime,
		ExecutionMS:   root.ExecutionTime,
		ActualRows:    int64(root.Plan.ActualRows),
		CountRead:     q.CountRead,
		CommitSHA:     sha,
		MeasuredAtUTC: time.Now().UTC(),
		PlanJSON:      json.RawMessage(payload),
	}, nil
}

// CheckBudget asserts a measurement against the declared ceiling for its scale.
func CheckBudget(b Budget, r Result) error {
	limit := b.WarmMaxMS
	if !r.Warm {
		limit = b.ColdMaxMS
	}
	if r.ExecutionMS > limit {
		return fmt.Errorf("%s at %s (%s): execution %.2fms exceeds budget %.2fms",
			r.Route, r.Scale, warmLabel(r.Warm), r.ExecutionMS, limit)
	}
	if r.CountRead {
		return nil
	}
	if r.ActualRows < b.MinRows {
		return fmt.Errorf("%s at %s: actual rows %d below minimum %d",
			r.Route, r.Scale, r.ActualRows, b.MinRows)
	}
	if b.MaxRows == 0 {
		if r.ActualRows != 0 {
			return fmt.Errorf("%s at %s: actual rows %d, want exact empty result",
				r.Route, r.Scale, r.ActualRows)
		}
		return nil
	}
	if r.ActualRows > b.MaxRows {
		return fmt.Errorf("%s at %s: actual rows %d above maximum %d",
			r.Route, r.Scale, r.ActualRows, b.MaxRows)
	}
	return nil
}

func warmLabel(warm bool) string {
	if warm {
		return "warm"
	}
	return "cold"
}

// BudgetFor returns the declared budget for a route at a scale.
func BudgetFor(scale Scale, route Route) (Budget, bool) {
	for _, b := range Budgets(scale) {
		if b.Route == route {
			return b, true
		}
	}
	return Budget{}, false
}
