package payment

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// syncBuffer is a log sink the sweeping goroutine writes and the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// unreachablePool is a pool whose every connection attempt fails at once and
// touches no socket, so a sweep run against it fails and logs without a
// database.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://goen@127.0.0.1:1/goen")
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	cfg.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		return errors.New("no database in this test")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	return pool
}

// A process restarted more often than the interval must still settle lapsed
// checkouts, so the first sweep runs when the loop starts and not a whole
// interval later. The sweep is seen through the line it logs when it fails.
func TestSessionSweepForeverSweepsAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		log := slog.New(slog.NewTextHandler(&out, nil))
		pool := unreachablePool(t)
		defer pool.Close()
		gateway, err := NewGateway("", "", "https://goen.test")
		if err != nil {
			t.Fatalf("gateway: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			NewSessionSweeper(NewStore(pool), gateway).SweepForever(ctx, log)
		}()

		const line = "sweep lapsed checkout sessions"
		synctest.Wait()
		if got := strings.Count(out.String(), line); got != 1 {
			t.Fatalf("sweeps before the first tick = %d, want 1\n%s", got, out.String())
		}

		time.Sleep(LapsedSessionInterval)
		synctest.Wait()
		if got := strings.Count(out.String(), line); got != 2 {
			t.Fatalf("sweeps after one interval = %d, want 2\n%s", got, out.String())
		}

		cancel()
		<-done
	})
}
