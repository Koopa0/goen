package ratelimit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Guard wraps a handler with a per-IP limit.
//
// Middleware and not a check inside the handler, because the ordering is the
// whole point: this must run before anything expensive, and a handler that
// checks partway down its own body is one refactor away from checking too late.
//
// It answers 429 with Retry-After. Not 403: the client is not forbidden, it is
// early, and a status that says so is what lets a well-behaved one back off
// instead of retrying immediately.
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
			Refuse(w, retryAfter)
			return
		}
		next(w, r)
	}
}

// Refuse writes the 429.
//
// Exported because the per-account check happens inside the sign-in handler,
// which has already read the form, and both refusals must look identical from
// outside. A 429 that differed by reason would answer "does this account
// exist?" — the question the whole sign-in flow is careful not to answer.
func Refuse(w http.ResponseWriter, retryAfter time.Duration) {
	// Rounded UP: Retry-After is whole seconds, and rounding down tells a
	// client to come back before it is allowed, which produces a second 429 and
	// looks like the limiter is broken.
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
	// i18n-exempt: refusing before the handler runs is the whole point — this
	// fires ahead of anything that could read a locale, and rendering a page
	// here would be work the limiter exists to avoid doing.
	_, _ = w.Write([]byte("429 請求過於頻繁,請稍後再試。\n"))
}
