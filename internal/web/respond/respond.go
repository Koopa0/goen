// Package respond is the HTTP answer written from a templ component, whole or
// as an htmx fragment, and the same-site reload hop page; it holds Component,
// IsHTMX and ReloadSameSite; it does not hold reading input, which is package
// formtext, or judging URLs, which is package siteurl.
package respond

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/web/pagestatus"
)

// IsHTMX reports whether htmx issued this request. Its absence means a plain
// browser navigation, so the handler must answer with a whole page.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Component writes c to the response with the given status, which the page can
// read from its context through package pagestatus, rendering into memory
// first: a template that fails halfway would otherwise leave a truncated body
// under an already-sent 200.
func Component(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, c templ.Component) {
	var buf bytes.Buffer
	if err := c.Render(pagestatus.With(r.Context(), status), &buf); err != nil {
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
