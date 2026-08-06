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

// MaxFormBytes bounds a form submission. goen's largest form is the contact
// message at 2,000 runes; 64 KiB leaves room for multi-byte text and the field
// names without letting an unbounded body reach ParseForm, which reads it all
// into memory before anything gets to reject it.
const MaxFormBytes = 64 << 10

// ParseForm reads a bounded form body.
//
// It exists so no handler can call r.ParseForm directly and forget the limit:
// the reason for the cap is not visible at the call site, and the failure it
// prevents only appears under load.
func ParseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxFormBytes)
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("parse form: %w", err)
	}
	return nil
}

// IsHTMX reports whether htmx issued this request. htmx sets the header on
// every request it makes, so its absence means a plain browser navigation and
// the handler must answer with a whole page.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Render writes c to the response with the given status.
//
// The component is rendered into memory first. A template that fails halfway
// through would otherwise leave a truncated body under an already-sent 200,
// which reads to the visitor as a broken page and to a monitor as a success.
func Render(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		log.ErrorContext(r.Context(), "render component", "error", err, "path", r.URL.Path)
		// i18n-exempt: the render itself failed, so there is no page to put a
		// translated message on and no guarantee the locale middleware ran. A
		// second render that may fail too is not the answer; both languages in
		// the literal is, so an English visitor is not left with a line only a
		// Chinese reader can use.
		http.Error(w, "500 內部錯誤 / Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.ErrorContext(r.Context(), "write response", "error", err, "path", r.URL.Path)
	}
}

// cartCountKey is unexported so nothing outside this package can put a value
// under it, which is what keeps [CartCount] honest about where the number came
// from.
type cartCountKey struct{}

// WithCartCount carries the visitor's cart size down to the page chrome.
//
// It goes through the CONTEXT rather than through every handler's view model.
// layouts.Page has carried a CartCount field since the header was built and
// nothing ever assigned it, so the badge read 0 for every visitor with any
// number of items in their cart — which is exactly how "each handler remembers
// to set it" fails. One middleware cannot forget.
func WithCartCount(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, cartCountKey{}, n)
}

// CartCount is how many items the visitor's cart holds, or zero when nothing
// put a count there — a page rendered outside the middleware, or a visitor with
// no cart.
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

// RequestID is that identifier, or "" outside a request.
//
// The audit trail stamps it onto every back-office row, so a row and the log
// lines from the same request can be put beside each other — which is the
// difference between "somebody published this" and knowing what else that
// request did.
func RequestID(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}
