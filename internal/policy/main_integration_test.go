//go:build integration

package policy

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestMain starts one server for the suite, so each test's dbtest.Pool is a
// copy of its template rather than a server of its own.
func TestMain(m *testing.M) {
	_, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}
