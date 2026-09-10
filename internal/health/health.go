// Package health answers the two questions an orchestrator asks: is this
// process alive, and should it be sent traffic. Liveness failing means "restart
// me"; readiness failing means "route around me for now".
package health

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Pinger reports whether a dependency is reachable. [*pgxpool.Pool] satisfies
// it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Dependency is one named thing readiness depends on. The name is what the log
// line and the response body report, so an operator learns which pool is down.
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
func NewHandler(log *slog.Logger, deps ...Dependency) *Handler {
	if log == nil || len(deps) == 0 {
		panic("health: NewHandler requires a logger and at least one dependency")
	}
	for _, d := range deps {
		if d.DB == nil || d.Name == "" {
			panic("health: every readiness dependency needs a name and a pool")
		}
	}
	return &Handler{deps: slices.Clone(deps), log: log}
}

// Live serves GET /healthz. It checks nothing: a liveness probe that tested the
// database would restart a healthy process because something else was down.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// Ready serves GET /readyz. goen cannot serve a page without PostgreSQL, so
// readiness is exactly whether every pool it serves from can answer.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// One budget for the whole check, not one per pool: a probe that hangs has
	// already failed.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	for _, d := range h.deps {
		if err := d.DB.Ping(ctx); err != nil {
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
