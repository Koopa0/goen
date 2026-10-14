//go:build integration

package dbtest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is what a probe needs: a pool, a connection or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Backend is one server process connected to the current database, as
// pg_stat_activity reports it.
type Backend struct {
	PID         int32
	Application string  // application_name
	State       string  // state; empty when unknown
	WaitType    string  // wait_event_type; empty when the backend is not waiting
	WaitEvent   string  // wait_event
	Query       string  // the backend's current or last statement
	BlockedBy   []int32 // pg_blocking_pids(pid)
}

// Match selects backends. A zero field matches every backend.
type Match struct {
	PID         int32   // only this backend
	Application string  // application_name equals this
	QueryLike   string  // query LIKE this pattern
	Active      bool    // state = 'active'
	BlockedBy   []int32 // at least one of these pids blocks the backend
}

// backendsSQL always restricts to the caller's database and excludes the
// caller's own backend. A test binary may share its PostgreSQL server with
// other databases, and a probe must never see their backends. A nil slice
// reaches the server as NULL, and cardinality(NULL) is NULL, so $5 is
// coalesced to match every backend when the caller names no blocker.
const backendsSQL = `
SELECT pid,
       coalesce(application_name, ''),
       coalesce(state, ''),
       coalesce(wait_event_type, ''),
       coalesce(wait_event, ''),
       coalesce(query, ''),
       pg_blocking_pids(pid)
FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid()
  AND ($1::integer = 0 OR pid = $1)
  AND ($2::text = '' OR application_name = $2)
  AND ($3::text = '' OR query LIKE $3)
  AND (NOT $4::boolean OR state = 'active')
  AND (coalesce(cardinality($5::integer[]), 0) = 0 OR pg_blocking_pids(pid) && $5::integer[])
  AND ($6::boolean OR wait_event_type = 'Lock')
ORDER BY pid`

// Backends returns the backends m selects among those connected to the
// current database, other than the caller's own, ordered by pid. It never
// reports a backend of another database on the same server.
func Backends(ctx context.Context, q Querier, m Match) ([]Backend, error) {
	return backends(ctx, q, m, true)
}

// Blocked returns the first backend m selects that is waiting on a lock
// (wait_event_type = 'Lock'), and false when none is.
func Blocked(ctx context.Context, q Querier, m Match) (Backend, bool, error) {
	found, err := backends(ctx, q, m, false)
	if err != nil || len(found) == 0 {
		return Backend{}, false, err
	}
	return found[0], true, nil
}

func backends(ctx context.Context, q Querier, m Match, anyWait bool) ([]Backend, error) {
	rows, err := q.Query(ctx, backendsSQL, m.PID, m.Application, m.QueryLike, m.Active, m.BlockedBy, anyWait)
	if err != nil {
		return nil, fmt.Errorf("dbtest: read backends: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Backend])
	if err != nil {
		return nil, fmt.Errorf("dbtest: read backends: %w", err)
	}
	return found, nil
}
