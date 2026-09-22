package telemetry

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var queryDuration metric.Float64Histogram

func initQueryInstruments(m metric.Meter) error {
	var err error
	queryDuration, err = m.Float64Histogram("goen.db.query.duration",
		metric.WithDescription("Database query duration by bounded operation and pool role"),
		metric.WithUnit("s"))
	return err
}

// QueryTracer separates query execution from pool admission without exporting
// SQL text, arguments, connection strings or database error details.
type QueryTracer struct{ Role PoolRole }

type queryTraceKey struct{}
type queryTrace struct {
	started   time.Time
	operation string
	span      trace.Span
}

func (t QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation := queryOperation(data.SQL)
	ctx, span := Tracer().Start(ctx, "postgres."+operation, //nolint:spancheck // pgx invokes TraceQueryEnd when the result closes.
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.role", string(t.Role)), attribute.String("db.operation", operation)))
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{started: time.Now(), operation: operation, span: span}) //nolint:spancheck // TraceQueryEnd owns the span.
}

func (t QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	q, ok := ctx.Value(queryTraceKey{}).(queryTrace)
	if !ok {
		return
	}
	outcome := "success"
	pgerr, isPGError := errors.AsType[*pgconn.PgError](data.Err)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(data.Err, context.DeadlineExceeded):
		outcome = "timeout"
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(data.Err, context.Canceled):
		outcome = "cancelled"
	case isPGError && pgerr.Code == "57014":
		outcome = "timeout"
	case data.Err != nil:
		outcome = "error"
	}
	attrs := []attribute.KeyValue{
		attribute.String("db.role", string(t.Role)),
		attribute.String("db.operation", q.operation),
		attribute.String("db.outcome", outcome),
	}
	if queryDuration != nil {
		queryDuration.Record(ctx, time.Since(q.started).Seconds(), metric.WithAttributes(attrs...))
	}
	q.span.SetAttributes(attribute.String("db.outcome", outcome))
	if outcome != "success" {
		q.span.SetStatus(codes.Error, outcome)
	}
	q.span.End()
}

func queryOperation(sql string) string {
	line, _, _ := strings.Cut(sql, "\n")
	// Only known source-owned names become dimensions; arbitrary SQL and
	// comments share one label, regardless of their values or query length.
	switch strings.TrimSpace(line) {
	case "-- name: ProductBySlug :one":
		return "product.read"
	case "-- name: ProductImages :many":
		return "product.images"
	case "-- name: ProductSpecs :many":
		return "product.specs"
	case "-- name: ProductVariants :many":
		return "product.variants"
	case "-- name: ProductOptions :many":
		return "product.options"
	case "-- name: CategoryAncestors :many":
		return "product.breadcrumb"
	default:
		return "other"
	}
}
