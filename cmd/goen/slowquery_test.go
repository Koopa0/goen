package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
