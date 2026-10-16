//go:build integration

package dbtest_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestMain(m *testing.M) {
	pool, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool.Close()
	code := m.Run()
	stop()
	os.Exit(code)
}
