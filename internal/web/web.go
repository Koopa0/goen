// Package web holds the HTTP concerns every goen feature shares: rendering a
// templ component, recognising an htmx request, and reading the request's
// cart count. It deliberately knows nothing about any single feature.
package web

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"
)

// MaxFormBytes bounds a form submission. ParseForm reads the whole body into
// memory before anything can reject it; goen's largest form is 2,000 runes.
const MaxFormBytes = 64 << 10

// ParseForm reads a bounded form body, so no handler calls r.ParseForm directly
// and forgets the limit.
func ParseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFormBytes)
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	return nil
}

// IsHTMX reports whether htmx issued this request. Its absence means a plain
// browser navigation, so the handler must answer with a whole page.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Render writes c to the response with the given status, rendering into memory
// first: a template that fails halfway would otherwise leave a truncated body
// under an already-sent 200.
func Render(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		log.ErrorContext(r.Context(), "render component", "error", err, "path", r.URL.Path)
		// i18n-exempt: the render itself failed, so there is no page to put a
		// translated message on and no guarantee the locale middleware ran.
		// Both languages in the literal, so neither reader is left out.
		http.Error(w, "500 內部錯誤 / Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.ErrorContext(r.Context(), "write response", "error", err, "path", r.URL.Path)
	}
}

// cartCountKey is unexported so nothing outside this package can write it.
type cartCountKey struct{}

// WithCartCount carries the visitor's cart size down to the page chrome, from
// middleware rather than from each handler's view model.
func WithCartCount(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, cartCountKey{}, n)
}

// CartCount is how many items the visitor's cart holds, or zero when nothing
// put a count there.
func CartCount(ctx context.Context) int {
	n, ok := ctx.Value(cartCountKey{}).(int)
	if !ok {
		return 0
	}
	return n
}

// pathKey carries the request's own path to the templates.
type pathKey struct{}

// WithRequestPath records where a request was for, so a form rendered on the
// page can send the visitor back to it.
func WithRequestPath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, pathKey{}, path)
}

// RequestPath is that path, or "" outside a request.
func RequestPath(ctx context.Context) string {
	p, ok := ctx.Value(pathKey{}).(string)
	if !ok {
		return ""
	}
	return p
}

// requestIDKey carries the request's identifier to a feature.
type requestIDKey struct{}

// WithRequestID records the identifier the log lines for this request carry.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID is that identifier, or "" outside a request. The audit trail stamps
// it onto every back-office row, so a row and its log lines can be put together.
func RequestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}
