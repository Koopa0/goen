package ratelimit

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// Guard wraps a handler with a per-IP limit, answering 429 with Retry-After.
// Middleware and not a check inside the handler, because it must run before
// anything expensive.
func Guard(l *Limiter, log *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	if l == nil || log == nil {
		panic("ratelimit: Guard requires a limiter and a logger")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		retryAfter, ok := l.Allow(ip)
		if !ok {
			log.WarnContext(r.Context(), "rate limited",
				"path", r.URL.Path, "retry_after_seconds", int(retryAfter.Seconds()+1))
			Refuse(r.Context(), w, retryAfter)
			return
		}
		next(w, r)
	}
}

// Refuse writes the 429. Exported so the sign-in handler's per-account refusal
// is byte-identical to the per-IP one, which is what stops it answering "does
// this account exist?".
func Refuse(ctx context.Context, w http.ResponseWriter, retryAfter time.Duration) {
	// Rounded UP: rounding down tells a client to come back before it is
	// allowed, which produces a second 429.
	seconds := int(retryAfter.Seconds())
	if retryAfter > time.Duration(seconds)*time.Second {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusTooManyRequests)
	// Plain text rather than a page, because rendering one is the work the
	// limiter exists to avoid — but in the visitor's own language: withLocale is
	// applied OUTSIDE the mux and every Guard is registered on it, so the locale
	// is on this context.
	_, _ = w.Write([]byte("429 " + i18n.T(ctx, i18n.KeyTooManyRequests) + "\n"))
}
