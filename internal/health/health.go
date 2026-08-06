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

// Handler answers the liveness and readiness probes.
type Handler struct {
	db  Pinger
	log *slog.Logger
}

// NewHandler returns a Handler that checks db for readiness.
func NewHandler(db Pinger, log *slog.Logger) *Handler {
	if db == nil || log == nil {
		panic("health: NewHandler requires a database and a logger")
	}
	return &Handler{db: db, log: log}
}

// Live serves GET /healthz. It checks nothing: reaching this handler is itself
// the proof that the process is running and serving. A liveness probe that
// tested the database would restart a healthy process because something else
// was down.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// Ready serves GET /readyz. goen cannot serve a page without PostgreSQL, so
// readiness is exactly whether the pool can answer.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Short: a probe that hangs is a probe that has already failed, and the
	// orchestrator's own timeout is less forgiving than any we would pick.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		h.log.WarnContext(ctx, "readiness probe failed", "error", err)
		writePlain(w, http.StatusServiceUnavailable, "database unreachable")
		return
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
