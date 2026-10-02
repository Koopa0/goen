//go:build integration

package taxonomy_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/media"
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

func handlerOver(s *taxonomy.Store) *taxonomy.Handler {
	log := slog.New(slog.DiscardHandler)
	return taxonomy.NewHandler(s, media.NewHandler(media.NewStore(pool), log), log)
}
