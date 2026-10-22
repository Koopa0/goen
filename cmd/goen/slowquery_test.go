package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/web/requestid"
)

// runTraced drives one statement through the tracer on a clock that has advanced by
// took between start and end, and returns what was logged.
func runTraced(t *testing.T, took time.Duration, sql string, args ...any) string {
	t.Helper()
	var out bytes.Buffer
	tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "admin")
	clock := time.Unix(1_700_000_000, 0)
	tr.now = func() time.Time { return clock }

	ctx := tr.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: sql, Args: args})
	clock = clock.Add(took)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	return out.String()
}

const namedStatement = "-- name: OrdersByEmail :many\nSELECT * FROM orders WHERE email = $1"

// TestSlowQueryTracerBelowThreshold: a statement shorter than
// slowQueryThreshold logs nothing.
func TestSlowQueryTracerBelowThreshold(t *testing.T) {
	t.Parallel()
	if got := runTraced(t, slowQueryThreshold-time.Millisecond, namedStatement, "x"); got != "" {
		t.Errorf("a statement under %v logged %q", slowQueryThreshold, got)
	}
}

// TestSlowQueryTracerAtThreshold: a statement of slowQueryThreshold logs a WARN
// naming the statement, its duration and the pool.
func TestSlowQueryTracerAtThreshold(t *testing.T) {
	t.Parallel()
	got := runTraced(t, slowQueryThreshold, namedStatement, "x")
	for _, want := range []string{
		"level=WARN", "statement=OrdersByEmail", "duration=500ms", "pool=admin",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("slow-query line %q lacks %q", got, want)
		}
	}
}

// TestSlowQueryTracerNoArgsOrSQL: the log line of a slow statement carries
// neither its arguments nor its statement text, only its sqlc name.
func TestSlowQueryTracerNoArgsOrSQL(t *testing.T) {
	t.Parallel()
	got := runTraced(t, 2*time.Second, namedStatement, "alice@example.com", 4242)
	for _, leaked := range []string{"alice@example.com", "4242", "SELECT", "email"} {
		if strings.Contains(got, leaked) {
			t.Errorf("slow-query line %q carries %q", got, leaked)
		}
	}
	if got == "" {
		t.Fatal("the slow statement was not logged at all")
	}
}

// TestSlowQueryTracerUnnamed: a statement with no sqlc name is logged as
// statement=unnamed, and its text is not logged.
func TestSlowQueryTracerUnnamed(t *testing.T) {
	t.Parallel()
	got := runTraced(t, time.Second, "SELECT 1 WHERE 'secret@example.com' = ''")
	if !strings.Contains(got, "statement=unnamed") || strings.Contains(got, "secret@example.com") {
		t.Errorf("unnamed statement logged %q", got)
	}
}

// TestSlowQueryTracerRequestIDs: slow queries started under different request
// IDs each log their own request_id, one started with none logs none, and no
// line carries the arguments, the SQL or the error text.
func TestSlowQueryTracerRequestIDs(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "store")
	clock := time.Unix(1_700_000_000, 0)
	tr.now = func() time.Time { return clock }
	starts := make([]context.Context, 0, 3)
	for _, id := range []string{"request-one", "request-two", ""} {
		ctx := t.Context()
		if id != "" {
			ctx = requestid.With(ctx, id)
		}
		starts = append(starts, tr.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: namedStatement, Args: []any{"private@example.com", 4242}}))
	}
	clock = clock.Add(slowQueryThreshold)
	for _, ctx := range starts {
		tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("private database error")})
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d warnings, want 3: %q", len(lines), out.String())
	}
	for i, id := range []string{"request-one", "request-two", ""} {
		for _, field := range []string{"level=WARN", "statement=OrdersByEmail", "pool=store", "duration=500ms"} {
			if !strings.Contains(lines[i], field) {
				t.Errorf("warning %q lacks %q", lines[i], field)
			}
		}
		if id == "" {
			if strings.Contains(lines[i], "request_id=") {
				t.Errorf("background warning invents an HTTP identity: %q", lines[i])
			}
		} else if !strings.Contains(lines[i], "request_id="+id) {
			t.Errorf("warning %q lost its request_id=%s", lines[i], id)
		}
		for _, leaked := range []string{"SELECT", "email =", "private@example.com", "4242", "private database error"} {
			if strings.Contains(lines[i], leaked) {
				t.Errorf("warning %q leaks %q", lines[i], leaked)
			}
		}
	}
}

// TestSlowQueryTracerAcquireThreshold: an acquire warns from 100 ms and not
// before, with its duration, pool and whether it failed, and without the error
// text or a request ID, whatever the error.
func TestSlowQueryTracerAcquireThreshold(t *testing.T) {
	t.Parallel()
	privateErr := errors.New("connect postgres://customer:private-password@db:5432/shop: private@example.com")
	for _, tc := range []struct {
		name     string
		took     time.Duration
		err      error
		duration string
		failed   string
	}{
		{name: "fast success", took: 100*time.Millisecond - time.Nanosecond},
		{name: "fast failure", took: 100*time.Millisecond - time.Nanosecond, err: privateErr},
		{name: "threshold success", took: 100 * time.Millisecond, duration: "100ms", failed: "false"},
		{name: "threshold failure", took: 100 * time.Millisecond, err: privateErr, duration: "100ms", failed: "true"},
		{name: "slow success", took: 2 * time.Second, duration: "2s", failed: "false"},
		{name: "slow cancellation", took: 2 * time.Second, err: context.Canceled, duration: "2s", failed: "true"},
		{name: "slow timeout", took: 2 * time.Second, err: context.DeadlineExceeded, duration: "2s", failed: "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "admin")
			clock := time.Unix(1_700_000_000, 0)
			tr.now = func() time.Time { return clock }
			var acquire pgxpool.AcquireTracer = tr
			ctx := acquire.TraceAcquireStart(t.Context(), nil, pgxpool.TraceAcquireStartData{})
			clock = clock.Add(tc.took)
			acquire.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{Err: tc.err})
			got := out.String()
			if tc.duration == "" {
				if got != "" {
					t.Errorf("fast acquire logged %q", got)
				}
				return
			}
			if strings.Count(got, "\n") != 1 {
				t.Fatalf("want one acquire warning, got %q", got)
			}
			for _, want := range []string{
				"level=WARN", `msg="slow acquire"`, "pool=admin", "duration=" + tc.duration, "failed=" + tc.failed,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("acquire warning %q lacks %q", got, want)
				}
			}
			for _, leaked := range []string{"postgres://", "private-password", "private@example.com", "context canceled", "context deadline exceeded", "request_id="} {
				if strings.Contains(got, leaked) {
					t.Errorf("acquire warning %q carries %q", got, leaked)
				}
			}
		})
	}
}

// TestSlowQueryTracerAcquireClocks: overlapping acquires keep separate clocks
// and request IDs: each warns with its own duration and its own request_id, and
// an acquire with no request ID logs none.
func TestSlowQueryTracerAcquireClocks(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "store")
	clock := time.Unix(1_700_000_000, 0)
	tr.now = func() time.Time { return clock }
	first := tr.TraceAcquireStart(requestid.With(t.Context(), "request-one"), nil, pgxpool.TraceAcquireStartData{})
	clock = clock.Add(50 * time.Millisecond)
	second := tr.TraceAcquireStart(requestid.With(t.Context(), "request-two"), nil, pgxpool.TraceAcquireStartData{})
	clock = clock.Add(50 * time.Millisecond)
	background := tr.TraceAcquireStart(t.Context(), nil, pgxpool.TraceAcquireStartData{})
	query := tr.TraceQueryStart(first, nil, pgx.TraceQueryStartData{SQL: namedStatement, Args: []any{"private@example.com"}})
	clock = clock.Add(100 * time.Millisecond)
	tr.TraceAcquireEnd(query, nil, pgxpool.TraceAcquireEndData{})
	tr.TraceAcquireEnd(second, nil, pgxpool.TraceAcquireEndData{})
	tr.TraceAcquireEnd(background, nil, pgxpool.TraceAcquireEndData{})
	tr.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d warnings, want 3: %q", len(lines), out.String())
	}
	for i, want := range []string{"duration=200ms request_id=request-one", "duration=150ms request_id=request-two", "duration=100ms"} {
		fields := strings.Fields(want)
		fields = append(fields, "level=WARN", `msg="slow acquire"`, "pool=store", "failed=false")
		for _, field := range fields {
			if !strings.Contains(lines[i], field) {
				t.Errorf("warning %q lacks %q", lines[i], field)
			}
		}
	}
	if strings.Contains(lines[2], "request_id=") {
		t.Errorf("background warning invents an HTTP identity: %q", lines[2])
	}
}

// TestSlowQueryTracerAcquireNoStart: TraceAcquireEnd for an acquire that never
// started logs nothing.
func TestSlowQueryTracerAcquireNoStart(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "maintenance")
	tr.TraceAcquireEnd(t.Context(), nil, pgxpool.TraceAcquireEndData{Err: context.Canceled})
	if got := out.String(); got != "" {
		t.Errorf("acquire without a start logged %q", got)
	}
}
