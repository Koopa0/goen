// Package web holds the HTTP concerns every goen feature shares: rendering a
// templ component, recognising an htmx request, and reading the request's
// cart count. It deliberately knows nothing about any single feature.
package web

import (
	"bytes"
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
		http.Error(w, "500 內部錯誤", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.ErrorContext(r.Context(), "write response", "error", err, "path", r.URL.Path)
	}
}
