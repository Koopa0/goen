//go:build integration

package dashboard_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/dashboard"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	if err := admintest.LoadCatalogue(context.Background(), pool); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

// storeOver builds the dashboard over p with the stock and reports desks as
// cmd/goen wires them and the given health desk, which a test replaces when it
// needs the health desk to fail.
func storeOver(p *pgxpool.Pool, desk dashboard.Health) *dashboard.Store {
	stockroom := stock.NewStore(p)
	return dashboard.NewStore(p, stockroom, desk, reports.NewStore(p, stockroom))
}

func handlerOver(s *dashboard.Store) *dashboard.Handler {
	return dashboard.NewHandler(s, slog.New(slog.DiscardHandler))
}
