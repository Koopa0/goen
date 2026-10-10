package main

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/web"
)

// slowQueryThreshold is the duration at which a statement is worth a line in
// the log: well under the statement timeouts, so a query creeping from 50ms to
// 800ms is seen long before it starts being cancelled.
const slowQueryThreshold = 500 * time.Millisecond

const slowAcquireThreshold = 100 * time.Millisecond

// sqlcName reads the `-- name: Foo :one` comment sqlc leaves at the head of
// every statement it generates.
var sqlcName = regexp.MustCompile(`(?m)^-- name: (\w+)`)

type slowQueryStart struct {
	at   time.Time
	name string
}

type slowQueryKey struct{}

type slowAcquireKey struct{}

// slowQueryTracer logs a WARN for a statement slower than slowQueryThreshold,
// naming the sqlc statement, and for a connection acquisition slower than
// slowAcquireThreshold, each with how long it took and which pool. It never
// logs the arguments, which carry customers' personal data, nor the error,
// whose text pgx builds from the offending values.
type slowQueryTracer struct {
	log       *slog.Logger
	pool      string
	threshold time.Duration
	now       func() time.Time
}

func newSlowQueryTracer(log *slog.Logger, pool string) *slowQueryTracer {
	return &slowQueryTracer{log: log, pool: pool, threshold: slowQueryThreshold, now: time.Now}
}

func (t *slowQueryTracer) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData,
) context.Context {
	name := "unnamed"
	if m := sqlcName.FindStringSubmatch(data.SQL); m != nil {
		name = m[1]
	}
	return context.WithValue(ctx, slowQueryKey{}, slowQueryStart{at: t.now(), name: name})
}

func (t *slowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	start, ok := ctx.Value(slowQueryKey{}).(slowQueryStart)
	if !ok {
		return
	}
	if elapsed := t.now().Sub(start.at); elapsed >= t.threshold {
		attrs := []slog.Attr{
			slog.String("statement", start.name),
			slog.Duration("duration", elapsed),
			slog.String("pool", t.pool),
		}
		if id := web.RequestID(ctx); id != "" {
			attrs = append(attrs, slog.String("request_id", id))
		}
		t.log.LogAttrs(ctx, slog.LevelWarn, "slow query", attrs...)
	}
}

func (t *slowQueryTracer) TraceAcquireStart(
	ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData,
) context.Context {
	return context.WithValue(ctx, slowAcquireKey{}, t.now())
}

func (t *slowQueryTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	start, ok := ctx.Value(slowAcquireKey{}).(time.Time)
	if !ok {
		return
	}
	if elapsed := t.now().Sub(start); elapsed >= slowAcquireThreshold {
		attrs := []slog.Attr{
			slog.String("pool", t.pool),
			slog.Duration("duration", elapsed),
			slog.Bool("failed", data.Err != nil),
		}
		if id := web.RequestID(ctx); id != "" {
			attrs = append(attrs, slog.String("request_id", id))
		}
		t.log.LogAttrs(ctx, slog.LevelWarn, "slow acquire", attrs...)
	}
}
