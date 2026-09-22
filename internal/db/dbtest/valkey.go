//go:build integration

package dbtest

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Valkey starts a Valkey container for one test and removes it afterwards.
func Valkey(t *testing.T) string {
	t.Helper()
	addr, stop, err := StartValkey(t.Context())
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}
	t.Cleanup(stop)
	return addr
}

// StartValkey brings up Valkey and returns a host:port address and teardown.
func StartValkey(ctx context.Context) (addr string, stop func(), err error) {
	container, err := valkey.Run(ctx, "valkey/valkey:8.0-alpine",
		testcontainers.WithCmdArgs("--maxmemory", "64mb", "--maxmemory-policy", "allkeys-lru"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return "", nil, fmt.Errorf("run valkey: %w", err)
	}
	terminate := func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			slog.Warn("dbtest: terminate valkey", "error", terr)
		}
	}
	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("valkey endpoint: %w", err)
	}
	return endpoint, terminate, nil
}
