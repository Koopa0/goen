package cart

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A host redeployed more often than the interval must still clear idle guest
// carts, so the first sweep runs when the loop starts. It is seen through the
// line it logs when it fails.
func TestSweepGuestCartsForeverSweepsAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var out syncBuffer
		log := slog.New(slog.NewTextHandler(&out, nil))
		pool := unreachablePool(t)
		defer pool.Close()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			NewStore(pool).SweepGuestCartsForever(ctx, log)
		}()

		synctest.Wait()
		if got := strings.Count(out.String(), "sweep idle guest carts"); got != 1 {
			t.Fatalf("sweeps before the first tick = %d, want 1\n%s", got, out.String())
		}

		time.Sleep(GuestCartSweepInterval)
		synctest.Wait()
		if got := strings.Count(out.String(), "sweep idle guest carts"); got != 2 {
			t.Fatalf("sweeps after one interval = %d, want 2\n%s", got, out.String())
		}

		cancel()
		<-done
	})
}

// The guest-cart period is a policy number: the cart cookie is set once at
// creation and lives exactly this long, so a shorter retention would delete a
// cart its owner can still open, and a longer one keeps what nobody can reach.
func TestGuestCartRetentionIsTheCookieLifetime(t *testing.T) {
	t.Parallel()
	if GuestCartRetain != 30*24*time.Hour {
		t.Errorf("GuestCartRetain = %v, want 30 days", GuestCartRetain)
	}
	if got := time.Duration(cookieMaxAge) * time.Second; got != GuestCartRetain {
		t.Errorf("cart cookie lives %v, retention is %v; they must be one number", got, GuestCartRetain)
	}
	if GuestCartSweepInterval > GuestCartRetain {
		t.Errorf("sweep interval %v exceeds the retention %v", GuestCartSweepInterval, GuestCartRetain)
	}
}
