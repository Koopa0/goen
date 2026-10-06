package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/home"
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

// TestChromeMiddlewareOfAnAbandonedRequestLogsNothing: the banner and nav reads
// fail with context.Canceled when the visitor leaves; that is no server fault,
// and the page behind them has no reader.
func TestChromeMiddlewareOfAnAbandonedRequestLogsNothing(t *testing.T) {
	t.Parallel()

	store := home.NewStore(ctxErrDB{})
	for name, wrap := range map[string]func(http.Handler, *slog.Logger) http.Handler{
		"banner": func(next http.Handler, log *slog.Logger) http.Handler { return withBanner(next, store, log, true) },
		"nav": func(next http.Handler, log *slog.Logger) http.Handler {
			return withTopNav(next, store, catalog.NewStore(ctxErrDB{}), log)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logs := &bytes.Buffer{}
			reached := false
			h := wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
				slog.New(slog.NewTextHandler(logs, nil)))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))

			if logs.Len() != 0 {
				t.Errorf("an abandoned request logged %q", logs.String())
			}
			if reached {
				t.Error("an abandoned request went on to render the page")
			}
		})
	}
}
