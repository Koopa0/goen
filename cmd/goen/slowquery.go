package main

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// slowQueryThreshold is the duration at which a statement is worth a line in
// the log: well under the statement timeouts, so a query creeping from 50ms to
// 800ms is seen long before it starts being cancelled.
const slowQueryThreshold = 500 * time.Millisecond

// sqlcName reads the `-- name: Foo :one` comment sqlc leaves at the head of
// every statement it generates.
var sqlcName = regexp.MustCompile(`(?m)^-- name: (\w+)`)

type slowQueryStart struct {
	at   time.Time
	name string
}

type slowQueryKey struct{}

// slowQueryTracer logs a WARN for a statement slower than threshold, naming the
// sqlc statement, how long it took and which pool ran it. It never logs the
// arguments, which carry customers' personal data, nor the error, whose text
// pgx builds from the offending values.
type slowQueryTracer struct {
	log       *slog.Logger
	pool      string
	threshold time.Duration
	now       func() time.Time
}

func newSlowQueryTracer(log *slog.Logger, pool string) *slowQueryTracer {
	return &slowQueryTracer{log: log, pool: pool, threshold: slowQueryThreshold, now: time.Now}
}

// TraceQueryStart implements pgx.QueryTracer.
func (t *slowQueryTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	name := "unnamed"
	if m := sqlcName.FindStringSubmatch(data.SQL); m != nil {
		name = m[1]
	}
	return context.WithValue(ctx, slowQueryKey{}, slowQueryStart{at: t.now(), name: name})
}

// TraceQueryEnd implements pgx.QueryTracer.
func (t *slowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	start, ok := ctx.Value(slowQueryKey{}).(slowQueryStart)
	if !ok {
		return
	}
	if elapsed := t.now().Sub(start.at); elapsed >= t.threshold {
		t.log.WarnContext(ctx, "slow query",
			"statement", start.name, "duration", elapsed, "pool", t.pool)
	}
}
