// Package health answers the two questions an orchestrator asks: is this
// process alive, and should it be sent traffic.
//
// They are different questions and conflating them is how a rolling deploy
// goes wrong. Liveness failing means "restart me". Readiness failing means
// "route around me for now" — a database that is briefly unreachable is the
// second, and answering it as the first turns a blip into a restart loop.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger reports whether a dependency is reachable. [*pgxpool.Pool] satisfies
// it; the interface exists here because this package cannot import the feature
// that owns the pool without a cycle, which is the cross-package case that
// justifies one.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Dependency is one named thing readiness depends on. The name is what the log
// line says, so an operator learns WHICH pool is down rather than that one is.
type Dependency struct {
	Name string
	DB   Pinger
}

// Handler answers the liveness and readiness probes.
type Handler struct {
	deps []Dependency
	log  *slog.Logger
}

// NewHandler returns a Handler that checks every dependency for readiness.
//
// EVERY pool, not just the storefront's. It took one, so a deployment whose
// admin DSN was wrong started cleanly, answered /readyz with 200, served the
// storefront — and 500ed the entire back office. The orchestrator was told to
// send traffic to a process that could not do half its job, and nothing would
// ever restart or drain it, because the one pool it was asked about was fine.
func NewHandler(log *slog.Logger, deps ...Dependency) *Handler {
	if log == nil || len(deps) == 0 {
		panic("health: NewHandler requires a logger and at least one dependency")
	}
	for _, d := range deps {
		if d.DB == nil || d.Name == "" {
			panic("health: every readiness dependency needs a name and a pool")
		}
	}
	return &Handler{deps: deps, log: log}
}

// Live serves GET /healthz. It checks nothing: reaching this handler is itself
// the proof that the process is running and serving. A liveness probe that
// tested the database would restart a healthy process because something else
// was down.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// Ready serves GET /readyz. goen cannot serve a page without PostgreSQL, so
// readiness is exactly whether every pool it serves from can answer.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Short: a probe that hangs is a probe that has already failed, and the
	// orchestrator's own timeout is less forgiving than any we would pick. The
	// budget covers the whole check rather than each pool, for the same reason.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	for _, d := range h.deps {
		if err := d.DB.Ping(ctx); err != nil {
			// Named, because "database unreachable" sent an operator to the
			// storefront's connection string while the admin one was wrong.
			h.log.WarnContext(ctx, "readiness probe failed", "pool", d.Name, "error", err)
			writePlain(w, http.StatusServiceUnavailable, d.Name+" database unreachable")
			return
		}
	}
	writePlain(w, http.StatusOK, "ready")
}

func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// Probes are polled; a cached answer is a stale answer.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
