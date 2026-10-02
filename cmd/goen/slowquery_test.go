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

	"github.com/koopa0/goen/internal/web"
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

func TestTheTracerStaysQuietBelowTheThreshold(t *testing.T) {
	t.Parallel()
	if got := runTraced(t, slowQueryThreshold-time.Millisecond, namedStatement, "x"); got != "" {
		t.Errorf("a statement under %v logged %q", slowQueryThreshold, got)
	}
}

func TestTheTracerWarnsAtTheThresholdNamingStatementDurationAndPool(t *testing.T) {
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

func TestTheTracerNeverLogsArgumentsOrStatementText(t *testing.T) {
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

func TestAStatementWithoutASqlcNameIsLoggedAsUnnamed(t *testing.T) {
	t.Parallel()
	got := runTraced(t, time.Second, "SELECT 1 WHERE 'secret@example.com' = ''")
	if !strings.Contains(got, "statement=unnamed") || strings.Contains(got, "secret@example.com") {
		t.Errorf("unnamed statement logged %q", got)
	}
}

func TestSlowQueriesKeepTheirOwnRequestIDsAndOmitBackgroundIdentity(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	tr := newSlowQueryTracer(slog.New(slog.NewTextHandler(&out, nil)), "store")
	clock := time.Unix(1_700_000_000, 0)
	tr.now = func() time.Time { return clock }
	starts := make([]context.Context, 0, 3)
	for _, id := range []string{"request-one", "request-two", ""} {
		ctx := t.Context()
		if id != "" {
			ctx = web.WithRequestID(ctx, id)
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
