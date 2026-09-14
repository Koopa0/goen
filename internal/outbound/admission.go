package outbound

import (
	"context"
	"errors"
	"sync"
)

var errAdmissionSaturated = errors.New("outbound: dependency admission saturated")

type admission struct {
	slots chan struct{}
}

var (
	admitMu sync.Mutex
	admit   = map[Dependency]*admission{}
)

func gate(dep Dependency) *admission {
	admitMu.Lock()
	defer admitMu.Unlock()
	if g := admit[dep]; g != nil {
		return g
	}
	n := maxActive(dep)
	g := &admission{slots: make(chan struct{}, n)}
	admit[dep] = g
	return g
}

func acquire(ctx context.Context, dep Dependency) error {
	g := gate(dep)
	select {
	case g.slots <- struct{}{}:
		observeActive(dep)
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errAdmissionSaturated
		}
		return ctx.Err()
	}
}

func release(dep Dependency) {
	g := gate(dep)
	select {
	case <-g.slots:
		observeActive(dep)
	default:
	}
}

// ResetAdmission clears in-flight admission state between tests.
func ResetAdmission() {
	admitMu.Lock()
	admit = map[Dependency]*admission{}
	admitMu.Unlock()
}
