// Package web holds the HTTP helpers every feature shares: rendering a templ
// component and htmx detection. It knows nothing about any single feature.
package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"
)

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
		if errors.Is(r.Context().Err(), context.Canceled) {
			// The caller left, so the render stopped and nobody reads a 500. A
			// deadline is not this: it stays an error below.
			return
		}
		log.ErrorContext(r.Context(), "render component", "error", err, "path", r.URL.Path)
		// i18n-exempt: the render failed, so there is no page to translate onto
		// and no guaranteed locale. Both languages, so neither reader is left out.
		http.Error(w, "500 內部錯誤 / Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.ErrorContext(r.Context(), "write response", "error", err, "path", r.URL.Path)
	}
}
