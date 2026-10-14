package issuer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
)

// failingDB is a pool whose every statement fails with err, as a dead admin
// pool or a role without EXECUTE on lease_invoice_operation does.
type failingDB struct{ err error }

func (f failingDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f.err
}

func (f failingDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, f.err
}

func (f failingDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return failingRow(f)
}

type failingRow struct{ err error }

func (r failingRow) Scan(...any) error { return r.err }

// stopAfterFirstLine collects the log output and cancels the loop once the
// first line is written, so the test needs neither a timer nor a sleep.
type stopAfterFirstLine struct {
	mu   sync.Mutex
	out  bytes.Buffer
	stop context.CancelFunc
}

func (w *stopAfterFirstLine) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.out.Write(p)
	w.stop()
	return n, err
}

func TestReconcileForeverLogsTheCauseWhenTheLeaseFails(t *testing.T) {
	t.Parallel()
	cause := errors.New("permission denied for function lease_invoice_operation")
	s := &Store{q: db.New(failingDB{err: cause}), gateway: &Gateway{}}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &stopAfterFirstLine{stop: cancel}
	s.ReconcileForever(ctx, slog.New(slog.NewJSONHandler(w, nil)))

	w.mu.Lock()
	defer w.mu.Unlock()
	lines := bytes.Split(bytes.TrimSpace(w.out.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want exactly the one deferral warning:\n%s", len(lines), w.out.String())
	}
	var rec map[string]any
	if err := json.Unmarshal(lines[0], &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, lines[0])
	}
	if rec["level"] != "WARN" || rec["msg"] != "invoice reconciliation deferred" {
		t.Fatalf("record = %v, want the deferral warning", rec)
	}
	got, ok := rec["error"].(string)
	if !ok {
		t.Fatalf("warning has no error attribute, so the operator cannot tell why nothing is leased: %v", rec)
	}
	if !strings.Contains(got, cause.Error()) {
		t.Errorf("error attribute = %q, want it to carry the lease failure %q", got, cause)
	}
	if rec["operation"] != uuid.Nil.String() {
		t.Errorf("operation = %v, want the zero id: nothing was leased", rec["operation"])
	}
}

func TestReconcileOnceNamesTheLeaseStageAndKeepsTheCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	s := &Store{q: db.New(failingDB{err: cause}), gateway: &Gateway{}}
	result, err := s.ReconcileOnce(t.Context())
	if !errors.Is(err, cause) {
		t.Fatalf("ReconcileOnce error = %v, want it to wrap %v", err, cause)
	}
	if result != (ReconcileResult{}) {
		t.Errorf("result = %+v, want the zero value: nothing was leased", result)
	}
}
