package product

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ctxErrDB fails every statement with the caller's context error, as pgx does
// once the request's context is done.
type ctxErrDB struct{}

func (ctxErrDB) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, ctx.Err()
}

func (ctxErrDB) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, ctx.Err()
}

func (ctxErrDB) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return ctxErrRow{ctx.Err()}
}

type ctxErrRow struct{ err error }

func (r ctxErrRow) Scan(...any) error { return r.err }

// TestDetailOfAnAbandonedRequestLogsNothing: a caller that left is not a failed
// product load, and nobody is there to read a 500.
func TestDetailOfAnAbandonedRequestLogsNothing(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	h := NewHandler(NewStore(ctxErrDB{}, log), log, "https://goen.example")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/nimbus-band-2", http.NoBody)
	r.SetPathValue("slug", "nimbus-band-2")
	w := httptest.NewRecorder()

	h.Detail(w, r)

	if logs.Len() != 0 {
		t.Errorf("an abandoned request logged %q", logs.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("an abandoned request was answered with %q", w.Body.String())
	}
}

// TestDetailStillLogsALoadThatFailsForGoensReason keeps the guard to the
// departed caller.
func TestDetailStillLogsALoadThatFailsForGoensReason(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	h := NewHandler(NewStore(ctxErrDB{}, log), log, "https://goen.example")
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/nimbus-band-2", http.NoBody)
	r.SetPathValue("slug", "nimbus-band-2")

	h.Detail(httptest.NewRecorder(), r)

	if !strings.Contains(logs.String(), "load product") {
		t.Errorf("a deadline was not logged as a load failure: %q", logs.String())
	}
}
