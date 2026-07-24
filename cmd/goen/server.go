package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/health"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/site"
)

// contentSecurityPolicy is deliberately strict: goen renders no inline script
// and no inline style, so neither needs an allowance. The two font hosts are
// the only third parties, and dropping them is the follow-up that self-hosting
// the faces would close.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data:; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

func newRouter(pool *pgxpool.Pool, log *slog.Logger) http.Handler {
	pages := site.NewHandler(log)
	probes := health.NewHandler(pool, log)
	messages := contact.NewHandler(contact.NewStore(pool), log)
	signups := newsletter.NewHandler(newsletter.NewStore(pool), log)

	mux := http.NewServeMux()
	mux.Handle("GET "+assets.Prefix, assets.Handler())

	// Probes come first because they must answer even when everything behind
	// them does not.
	mux.HandleFunc("GET /healthz", probes.Live)
	mux.HandleFunc("GET /readyz", probes.Ready)

	mux.HandleFunc("GET /{$}", pages.Home)
	mux.HandleFunc("GET /about", pages.About)
	mux.HandleFunc("GET /contact", messages.Page)
	mux.HandleFunc("POST /contact", messages.Submit)
	mux.HandleFunc("POST /newsletter", signups.Submit)
	mux.HandleFunc("GET /newsletter/thanks", signups.Thanks)

	// Everything the storefront does not serve yet, including the routes the
	// header and footer already link to.
	mux.HandleFunc("GET /", pages.NotFound)

	// Applied inner to outer, so a request passes through them in the reverse
	// of this order: recover, then tag with an id, then log, then the security
	// headers, then the cross-origin check. The id is attached before logging
	// so every line about one request carries the same one, and before the
	// recover handler unwinds so a panic is traceable to its request.
	var handler http.Handler = mux
	handler = crossOriginProtection(handler)
	handler = securityHeaders(handler)
	handler = requestLog(handler, log)
	handler = withRequestID(handler)
	return recoverPanic(handler, log)
}

// crossOriginProtection rejects cross-site form posts using the browser's own
// Sec-Fetch-Site signal, which is why goen's forms carry no CSRF token.
func crossOriginProtection(next http.Handler) http.Handler {
	return http.NewCrossOriginProtection().Handler(next)
}

// requestIDKey is unexported so nothing outside this package can put a value
// under it, which is what keeps [RequestID] honest about where the id came
// from.
type requestIDKey struct{}

// withRequestID gives every request an identifier and echoes it back.
//
// A client-supplied X-Request-Id is honoured so a trace can span a proxy, but
// only when it looks like an id: an arbitrary header value would otherwise
// reach the logs, where an attacker-chosen newline could forge a log line.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// validRequestID accepts the shape an id may take: printable ASCII, bounded,
// and nothing that could break a log line.
func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		alphanumeric := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !alphanumeric && c != '-' {
			return false
		}
	}
	return true
}

// requestID returns the identifier attached to r, or "" outside the chain —
// which happens in a test that calls a handler directly.
func requestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("request_id", requestID(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.statusCode()),
			slog.Duration("took", time.Since(start)),
		)
	})
}

func recoverPanic(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic serving request",
					"request_id", requestID(r.Context()),
					"panic", v,
					"method", r.Method,
					"path", r.URL.Path,
				)
				// The handler may already have written; this is best effort.
				http.Error(w, "500 內部錯誤", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status a handler sent so the request log can
// report it.
//
// It implements only WriteHeader and Write. Flush and Hijack reach the real
// writer through Unwrap, which [http.ResponseController] follows, so wrapping
// does not remove a capability from anything downstream.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// statusCode reports the status sent, treating a handler that wrote nothing at
// all as the 200 net/http sends on its behalf.
func (s *statusRecorder) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}
