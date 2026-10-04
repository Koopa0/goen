//go:build integration

package returns_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/returns"
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

// storeOver builds the returns desk and the refunds it pays through over one
// pool and one refunder, as cmd/goen does.
func storeOver(p *pgxpool.Pool, refunder refunds.Refunder) *returns.Store {
	return returns.NewStore(p, refunds.NewStore(p, refunder, nil))
}

func handlerOver(s *returns.Store) *returns.Handler {
	return returns.NewHandler(s, slog.New(slog.DiscardHandler))
}
