// Package probe answers the two questions an orchestrator asks: is this
// process alive, and should it be sent traffic. Liveness failing means "restart
// me"; readiness failing means "route around me for now".
package probe

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Pinger reports whether a dependency is reachable. [*pgxpool.Pool] satisfies
// it.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	pool   Pinger
	schema func(context.Context) error
	log    *slog.Logger
}

// NewHandler answers for the storefront pool. schema reports whether the
// database is the schema this binary was built with: a process on another
// schema would answer 500 for whatever the two disagree on, so it is not ready.
func NewHandler(log *slog.Logger, pool Pinger, schema func(context.Context) error) *Handler {
	if log == nil || pool == nil || schema == nil {
		panic("probe: NewHandler requires a logger, the storefront pool and its schema check")
	}
	return &Handler{pool: pool, schema: schema, log: log}
}

// Live serves GET /healthz. It checks nothing: a liveness probe that tested the
// database would restart a healthy process because something else was down.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

// Ready serves GET /readyz. A degraded back office must not take shopper routes
// out of service.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		h.log.WarnContext(ctx, "readiness probe failed", "pool", "storefront", "error", err)
		writePlain(w, http.StatusServiceUnavailable, "storefront database unreachable")
		return
	}
	if err := h.schema(ctx); err != nil {
		h.log.WarnContext(ctx, "readiness probe failed", "pool", "storefront", "error", err)
		writePlain(w, http.StatusServiceUnavailable, "database schema is not this binary's")
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
